#include "miniapp_frida.h"
#include "frida-core.h"
#include <windows.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef unsigned char mb_z_byte;
typedef unsigned long mb_z_ulong;
extern const char *zlibVersion(void);
extern mb_z_ulong compressBound(mb_z_ulong source_length);
extern int compress2(mb_z_byte *dest, mb_z_ulong *dest_length, const mb_z_byte *source, mb_z_ulong source_length, int level);
extern int uncompress2(mb_z_byte *dest, mb_z_ulong *dest_length, const mb_z_byte *source, mb_z_ulong *source_length);

#define MB_Z_OK 0
#define MB_Z_BUF_ERROR (-5)
#define MB_Z_DEFAULT_COMPRESSION (-1)
#define MB_MAX_ZLIB_OUTPUT ((size_t)(256u * 1024u * 1024u))
#define MB_NATIVE_DEADLINE_MS 15000u

typedef struct {
  GMutex mutex;
  GCond drained;
  gboolean closing;
  guint in_flight;
  uintptr_t handle;
} mb_callback_owner;

struct mb_device { FridaDeviceManager *manager; FridaDevice *device; };
struct mb_session { FridaSession *session; mb_callback_owner callback; mb_detached_cb detached; };
struct mb_script { FridaScript *script; mb_callback_owner callback; mb_message_cb message; };
static volatile gint mb_frida_refs = 0;
static volatile gint mb_frida_initialized = 0;
static volatile gint mb_frida_owned = 0;
static SRWLOCK mb_frida_runtime_lock = SRWLOCK_INIT;

typedef struct {
  HANDLE stop;
  HANDLE thread;
  GCancellable *cancellable;
} mb_native_deadline;

uint32_t mb_abi_version(void) { return MB_ABI_VERSION; }
const char *mb_native_version(void) { return MB_NATIVE_VERSION; }
const char *mb_frida_core_version(void) { return MB_FRIDA_CORE_VERSION; }
const char *mb_zlib_version(void) { return zlibVersion(); }

static void mb_set_text_error(char **out, const char *message) {
  if (out != NULL) *out = _strdup(message != NULL ? message : "native operation failed");
}

static DWORD WINAPI mb_native_deadline_worker(void *data) {
  mb_native_deadline *deadline = data;
  if (WaitForSingleObject(deadline->stop, MB_NATIVE_DEADLINE_MS) == WAIT_TIMEOUT)
    g_cancellable_cancel(deadline->cancellable);
  return 0;
}

static gboolean mb_native_deadline_start(mb_native_deadline *deadline, char **error) {
  memset(deadline, 0, sizeof(*deadline));
  deadline->cancellable = g_cancellable_new();
  if (deadline->cancellable == NULL) {
    mb_set_text_error(error, "native deadline cancellable allocation failed");
    return FALSE;
  }
  deadline->stop = CreateEventW(NULL, TRUE, FALSE, NULL);
  if (deadline->stop == NULL) {
    g_object_unref(deadline->cancellable);
    deadline->cancellable = NULL;
    mb_set_text_error(error, "native deadline stop event creation failed");
    return FALSE;
  }
  deadline->thread = CreateThread(NULL, 0, mb_native_deadline_worker, deadline, 0, NULL);
  if (deadline->thread == NULL) {
    CloseHandle(deadline->stop);
    g_object_unref(deadline->cancellable);
    memset(deadline, 0, sizeof(*deadline));
    mb_set_text_error(error, "native deadline watchdog creation failed");
    return FALSE;
  }
  return TRUE;
}

static void mb_native_deadline_stop(mb_native_deadline *deadline) {
  SetEvent(deadline->stop);
  WaitForSingleObject(deadline->thread, INFINITE);
  CloseHandle(deadline->thread);
  CloseHandle(deadline->stop);
  g_object_unref(deadline->cancellable);
  memset(deadline, 0, sizeof(*deadline));
}

int mb_zlib_compress(const uint8_t *input, size_t input_size, uint8_t **output, size_t *output_size, char **error) {
  static const uint8_t empty = 0;
  if (output == NULL || output_size == NULL) { mb_set_text_error(error, "zlib output arguments are required"); return 0; }
  *output = NULL; *output_size = 0;
  if ((input == NULL && input_size != 0) || input_size > ULONG_MAX) { mb_set_text_error(error, "zlib input is invalid or too large"); return 0; }
  mb_z_ulong capacity = compressBound((mb_z_ulong) input_size);
  if (capacity == 0) capacity = 1;
  if ((size_t) capacity > MB_MAX_ZLIB_OUTPUT) { mb_set_text_error(error, "zlib output limit exceeded"); return 0; }
  uint8_t *buffer = malloc((size_t) capacity);
  if (buffer == NULL) { mb_set_text_error(error, "zlib output allocation failed"); return 0; }
  mb_z_ulong size = capacity;
  int result = compress2(buffer, &size, input != NULL ? input : &empty, (mb_z_ulong) input_size, MB_Z_DEFAULT_COMPRESSION);
  if (result != MB_Z_OK) {
    char message[96]; snprintf(message, sizeof(message), "zlib compress failed: %d", result);
    free(buffer); mb_set_text_error(error, message); return 0;
  }
  *output = buffer; *output_size = (size_t) size; return 1;
}

int mb_zlib_decompress(const uint8_t *input, size_t input_size, size_t expected_size, size_t max_output, uint8_t **output, size_t *output_size, char **error) {
  static const uint8_t empty = 0;
  if (output == NULL || output_size == NULL) { mb_set_text_error(error, "zlib output arguments are required"); return 0; }
  *output = NULL; *output_size = 0;
  if ((input == NULL && input_size != 0) || input_size > ULONG_MAX || max_output == 0 || max_output > ULONG_MAX ||
      expected_size > max_output || expected_size > MB_MAX_ZLIB_OUTPUT) {
    mb_set_text_error(error, "zlib input or output limit is invalid"); return 0;
  }
  if (max_output > MB_MAX_ZLIB_OUTPUT) max_output = MB_MAX_ZLIB_OUTPUT;
  size_t capacity = expected_size;
  if (capacity == 0) {
    capacity = max_output <= 256 ? max_output : (input_size <= (max_output - 256) / 4 ? input_size * 4 + 256 : max_output);
    if (capacity > max_output) capacity = max_output;
  }
  if (capacity == 0) capacity = 1;
  for (;;) {
    uint8_t *buffer = malloc(capacity);
    if (buffer == NULL) { mb_set_text_error(error, "zlib output allocation failed"); return 0; }
    mb_z_ulong size = (mb_z_ulong) capacity;
    mb_z_ulong source_size = (mb_z_ulong) input_size;
    int result = uncompress2(buffer, &size, input != NULL ? input : &empty, &source_size);
    if (result == MB_Z_OK) {
      if (expected_size != 0 && size != (mb_z_ulong) expected_size) {
        char message[128]; snprintf(message, sizeof(message), "zlib decompressed size mismatch: expected=%zu actual=%lu", expected_size, size);
        free(buffer); mb_set_text_error(error, message); return 0;
      }
      *output = buffer; *output_size = (size_t) size; return 1;
    }
    free(buffer);
    if (result != MB_Z_BUF_ERROR || expected_size != 0 || capacity >= max_output) {
      char message[96]; snprintf(message, sizeof(message), "zlib decompress failed: %d", result);
      mb_set_text_error(error, message); return 0;
    }
    capacity = capacity > max_output / 2 ? max_output : capacity * 2;
  }
}

void mb_bytes_free(uint8_t *bytes) { free(bytes); }

static void mb_frida_release(void) {
  AcquireSRWLockExclusive(&mb_frida_runtime_lock);
  if (g_atomic_int_get(&mb_frida_refs) > 0) g_atomic_int_add(&mb_frida_refs, -1);
  ReleaseSRWLockExclusive(&mb_frida_runtime_lock);
}

static gboolean mb_frida_acquire(char **error) {
  gboolean acquired = FALSE;
  AcquireSRWLockExclusive(&mb_frida_runtime_lock);
  if (g_atomic_int_get(&mb_frida_initialized) == 0) {
    frida_init();
    g_atomic_int_set(&mb_frida_initialized, 1);
    g_atomic_int_set(&mb_frida_owned, 1);
  }
  if (g_atomic_int_get(&mb_frida_initialized) != 0) {
    g_atomic_int_inc(&mb_frida_refs);
    acquired = TRUE;
  } else {
    mb_set_text_error(error, "frida runtime initialization failed");
  }
  ReleaseSRWLockExclusive(&mb_frida_runtime_lock);
  return acquired;
}

static void mb_set_error(char **out, GError *error) {
  if (out != NULL) *out = _strdup(error != NULL ? error->message : "frida operation failed");
  if (error != NULL) g_error_free(error);
}
static void mb_callback_owner_init(mb_callback_owner *owner, uintptr_t handle) {
  g_mutex_init(&owner->mutex);
  g_cond_init(&owner->drained);
  owner->closing = FALSE;
  owner->in_flight = 0;
  owner->handle = handle;
}
static gboolean mb_callback_owner_enter(mb_callback_owner *owner, uintptr_t *handle) {
  gboolean entered = FALSE;
  g_mutex_lock(&owner->mutex);
  if (!owner->closing) {
    owner->in_flight++;
    *handle = owner->handle;
    entered = TRUE;
  }
  g_mutex_unlock(&owner->mutex);
  return entered;
}
static void mb_callback_owner_leave(mb_callback_owner *owner) {
  g_mutex_lock(&owner->mutex);
  g_assert(owner->in_flight > 0);
  owner->in_flight--;
  if (owner->closing && owner->in_flight == 0) g_cond_broadcast(&owner->drained);
  g_mutex_unlock(&owner->mutex);
}
static void mb_callback_owner_close(mb_callback_owner *owner) {
  g_mutex_lock(&owner->mutex);
  owner->closing = TRUE;
  g_mutex_unlock(&owner->mutex);
}
static void mb_callback_owner_drain(mb_callback_owner *owner) {
  g_mutex_lock(&owner->mutex);
  while (owner->in_flight != 0) g_cond_wait(&owner->drained, &owner->mutex);
  g_mutex_unlock(&owner->mutex);
}
static void mb_callback_owner_clear(mb_callback_owner *owner) {
  g_cond_clear(&owner->drained);
  g_mutex_clear(&owner->mutex);
}
static uint32_t mb_process_ppid(FridaProcess *process) {
  GHashTable *params = frida_process_get_parameters(process);
  GVariant *v = params != NULL ? g_hash_table_lookup(params, "ppid") : NULL;
  if (v == NULL) return 0;
  if (g_variant_is_of_type(v, G_VARIANT_TYPE_INT32)) return (uint32_t) g_variant_get_int32(v);
  if (g_variant_is_of_type(v, G_VARIANT_TYPE_INT64)) return (uint32_t) g_variant_get_int64(v);
  if (g_variant_is_of_type(v, G_VARIANT_TYPE_UINT32)) return g_variant_get_uint32(v);
  if (g_variant_is_of_type(v, G_VARIANT_TYPE_UINT64)) return (uint32_t) g_variant_get_uint64(v);
  return 0;
}
static char *mb_process_path(FridaProcess *process) {
  GHashTable *params = frida_process_get_parameters(process);
  GVariant *v = params != NULL ? g_hash_table_lookup(params, "path") : NULL;
  if (v == NULL || !g_variant_is_of_type(v, G_VARIANT_TYPE_STRING)) return _strdup("");
  return _strdup(g_variant_get_string(v, NULL));
}
static void mb_on_message(FridaScript *script, const gchar *message, GBytes *data, gpointer user_data) {
  mb_script *owner = user_data; gsize size = 0; uintptr_t handle = 0; (void) script;
  if (!mb_callback_owner_enter(&owner->callback, &handle)) return;
  const guint8 *bytes = data != NULL ? g_bytes_get_data(data, &size) : NULL;
  if (owner->message != NULL) owner->message(handle, (char *) message, (uint8_t *) bytes, size);
  mb_callback_owner_leave(&owner->callback);
}
static void mb_on_detached(FridaSession *session, FridaSessionDetachReason reason, FridaCrash *crash, gpointer user_data) {
  mb_session *owner = user_data; uintptr_t handle = 0; (void) session; (void) crash;
  if (!mb_callback_owner_enter(&owner->callback, &handle)) return;
  if (owner->detached != NULL) owner->detached(handle, (int) reason);
  mb_callback_owner_leave(&owner->callback);
}

mb_device *mb_device_open(char **error) {
  GError *native_error = NULL; mb_native_deadline deadline;
  if (!mb_frida_acquire(error)) return NULL;
  mb_device *owner = calloc(1, sizeof(mb_device));
  if (owner == NULL) { if (error != NULL) *error = _strdup("out of memory"); mb_frida_release(); return NULL; }
  owner->manager = frida_device_manager_new();
  if (!mb_native_deadline_start(&deadline, error)) { frida_unref(owner->manager); free(owner); mb_frida_release(); return NULL; }
  owner->device = frida_device_manager_get_device_by_type_sync(owner->manager, FRIDA_DEVICE_TYPE_LOCAL, (gint)MB_NATIVE_DEADLINE_MS, deadline.cancellable, &native_error);
  mb_native_deadline_stop(&deadline);
  if (owner->device == NULL) { mb_set_error(error, native_error); mb_device_close(owner); return NULL; }
  return owner;
}
int mb_device_enumerate(mb_device *device, mb_process **items, size_t *count, char **error) {
  GError *native_error = NULL; mb_native_deadline deadline; FridaProcessQueryOptions *options = frida_process_query_options_new();
  frida_process_query_options_set_scope(options, FRIDA_SCOPE_METADATA);
  if (!mb_native_deadline_start(&deadline, error)) { frida_unref(options); return 0; }
  FridaProcessList *list = frida_device_enumerate_processes_sync(device->device, options, deadline.cancellable, &native_error);
  mb_native_deadline_stop(&deadline);
  frida_unref(options);
  if (list == NULL) { mb_set_error(error, native_error); return 0; }
  size_t n = (size_t) frida_process_list_size(list); mb_process *result = calloc(n, sizeof(mb_process));
  if (result == NULL && n != 0) { frida_unref(list); if (error != NULL) *error = _strdup("out of memory"); return 0; }
  for (size_t i = 0; i != n; i++) {
    FridaProcess *process = frida_process_list_get(list, (gint) i);
    result[i].pid = frida_process_get_pid(process); result[i].ppid = mb_process_ppid(process);
    result[i].name = _strdup(frida_process_get_name(process)); result[i].path = mb_process_path(process);
    frida_unref(process);
  }
  frida_unref(list); *items = result; *count = n; return 1;
}
void mb_processes_free(mb_process *items, size_t count) { if (items == NULL) return; for (size_t i=0;i<count;i++){free(items[i].name);free(items[i].path);} free(items); }
mb_session *mb_device_attach(mb_device *device, uint32_t pid, uintptr_t handle, mb_detached_cb callback, char **error) {
  GError *native_error = NULL; mb_native_deadline deadline;
  if (!mb_native_deadline_start(&deadline, error)) return NULL;
  FridaSession *session = frida_device_attach_sync(device->device, pid, NULL, deadline.cancellable, &native_error);
  if (session == NULL) { mb_native_deadline_stop(&deadline); mb_set_error(error, native_error); return NULL; }
  mb_session *owner = calloc(1, sizeof(mb_session));
  if (owner == NULL) {
    frida_session_detach_sync(session, deadline.cancellable, NULL);
    frida_unref(session);
    mb_native_deadline_stop(&deadline);
    mb_set_text_error(error, "out of memory");
    return NULL;
  }
  owner->session=session; owner->detached=callback; mb_callback_owner_init(&owner->callback, handle);
  g_signal_connect_data(session, "detached", G_CALLBACK(mb_on_detached), owner, NULL, 0); mb_native_deadline_stop(&deadline); return owner;
}
void mb_device_close(mb_device *device) { if(device==NULL)return; if(device->device)frida_unref(device->device); if(device->manager){mb_native_deadline deadline;if(mb_native_deadline_start(&deadline,NULL)){frida_device_manager_close_sync(device->manager,deadline.cancellable,NULL);mb_native_deadline_stop(&deadline);}frida_unref(device->manager);} free(device); mb_frida_release(); }
void mb_runtime_shutdown(void) {
  AcquireSRWLockExclusive(&mb_frida_runtime_lock);
  if (g_atomic_int_get(&mb_frida_refs) == 0 && g_atomic_int_get(&mb_frida_initialized) != 0 && g_atomic_int_get(&mb_frida_owned) != 0) {
    g_atomic_int_set(&mb_frida_initialized, 0);
    g_atomic_int_set(&mb_frida_owned, 0);
    frida_deinit();
  }
  ReleaseSRWLockExclusive(&mb_frida_runtime_lock);
}
mb_script *mb_session_load_script(mb_session *session, const char *source, uintptr_t handle, mb_message_cb callback, char **error) {
  GError *native_error=NULL; mb_native_deadline deadline; FridaScriptOptions *options=frida_script_options_new();
  if(!mb_native_deadline_start(&deadline,error)){frida_unref(options);return NULL;}
  FridaScript *script=frida_session_create_script_sync(session->session,source,options,deadline.cancellable,&native_error); frida_unref(options);
  if(script==NULL){mb_native_deadline_stop(&deadline);mb_set_error(error,native_error);return NULL;}
  mb_script *owner=calloc(1,sizeof(mb_script));
  if(owner==NULL){frida_unref(script);mb_native_deadline_stop(&deadline);mb_set_text_error(error,"out of memory");return NULL;}
  owner->script=script;owner->message=callback;mb_callback_owner_init(&owner->callback,handle);
  g_signal_connect_data(script,"message",G_CALLBACK(mb_on_message),owner,NULL,0); frida_script_load_sync(script,deadline.cancellable,&native_error);
  mb_native_deadline_stop(&deadline);
  if(native_error!=NULL){mb_set_error(error,native_error);mb_callback_owner_close(&owner->callback);g_signal_handlers_disconnect_by_data(script,owner);mb_callback_owner_drain(&owner->callback);frida_unref(script);mb_callback_owner_clear(&owner->callback);free(owner);return NULL;} return owner;
}
int mb_session_detach(mb_session *session, char **error) { if(session==NULL)return 1; GError *e=NULL;mb_native_deadline deadline;mb_callback_owner_close(&session->callback);g_signal_handlers_disconnect_by_data(session->session,session);if(mb_native_deadline_start(&deadline,error)){frida_session_detach_sync(session->session,deadline.cancellable,&e);mb_native_deadline_stop(&deadline);}mb_callback_owner_drain(&session->callback);frida_unref(session->session);mb_callback_owner_clear(&session->callback);free(session);if(e){mb_set_error(error,e);return 0;}return error==NULL||*error==NULL; }
int mb_script_post(mb_script *script, const char *json, char **error) { (void)error; if(script==NULL)return 0;frida_script_post(script->script,json,NULL);return 1; }
int mb_script_unload(mb_script *script, char **error) { if(script==NULL)return 1;GError *e=NULL;mb_native_deadline deadline;mb_callback_owner_close(&script->callback);g_signal_handlers_disconnect_by_data(script->script,script);if(mb_native_deadline_start(&deadline,error)){frida_script_unload_sync(script->script,deadline.cancellable,&e);mb_native_deadline_stop(&deadline);}mb_callback_owner_drain(&script->callback);frida_unref(script->script);mb_callback_owner_clear(&script->callback);free(script);if(e){mb_set_error(error,e);return 0;}return error==NULL||*error==NULL; }
void mb_error_free(char *error) { free(error); }
