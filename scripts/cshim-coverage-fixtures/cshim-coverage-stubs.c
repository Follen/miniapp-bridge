#include "cshim-coverage-stubs.h"

#include <stdio.h>

#undef CreateEventW
#undef CreateThread
#undef WaitForSingleObject

int mb_cov_fail_init = 0;
int mb_cov_fail_device = 0;
int mb_cov_fail_enumerate = 0;
int mb_cov_fail_attach = 0;
int mb_cov_fail_create_script = 0;
int mb_cov_fail_load_script = 0;
int mb_cov_fail_detach = 0;
int mb_cov_fail_unload = 0;
int mb_cov_fail_compress = 0;
int mb_cov_decompress_mode = 0;
int mb_cov_decompress_calls = 0;
int mb_cov_fail_malloc_once = 0;
int mb_cov_fail_calloc_once = 0;
guint *mb_cov_drain_in_flight = NULL;
int mb_cov_fail_cancellable = 0;
int mb_cov_fail_deadline_event = 0;
int mb_cov_fail_deadline_thread = 0;
int mb_cov_force_deadline_timeout = 0;
int mb_cov_wait_for_cancel = 0;
volatile LONG mb_cov_cancel_count = 0;
volatile LONG mb_cov_watchdogs_active = 0;

mb_cov_message_handler mb_cov_message_callback = NULL;
mb_cov_detached_handler mb_cov_detached_callback = NULL;
gpointer mb_cov_message_user_data = NULL;
gpointer mb_cov_detached_user_data = NULL;

static FridaDeviceManager manager = {1};
static FridaDevice device = {1};
static FridaSession session = {1};
static FridaScript script = {1};
static FridaProcess processes[6];
static FridaProcessList process_list;
static int processes_initialized = 0;

static void set_error(GError **out, const char *message) {
  if (out == NULL) return;
  GError *error = malloc(sizeof(*error));
  if (error == NULL) {
    *out = NULL;
    return;
  }
  error->message = strdup(message);
  *out = error;
}

void *mb_cov_malloc(size_t size) {
  if (mb_cov_fail_malloc_once) {
    mb_cov_fail_malloc_once = 0;
    return NULL;
  }
  return malloc(size);
}

void *mb_cov_calloc(size_t count, size_t size) {
  if (mb_cov_fail_calloc_once) {
    mb_cov_fail_calloc_once = 0;
    return NULL;
  }
  return calloc(count, size);
}

void mb_cov_free(void *value) { free(value); }

char *mb_cov_strdup(const char *value) {
  if (value == NULL) value = "";
  size_t length = strlen(value) + 1;
  char *copy = malloc(length);
  if (copy != NULL) memcpy(copy, value, length);
  return copy;
}

void g_mutex_init(GMutex *mutex) { mutex->unused = 0; }
void g_mutex_clear(GMutex *mutex) { mutex->unused = 0; }
void g_mutex_lock(GMutex *mutex) { (void)mutex; }
void g_mutex_unlock(GMutex *mutex) { (void)mutex; }
void g_cond_init(GCond *condition) { condition->unused = 0; }
void g_cond_clear(GCond *condition) { condition->unused = 0; }
void g_cond_wait(GCond *condition, GMutex *mutex) {
  (void)condition;
  (void)mutex;
  if (mb_cov_drain_in_flight != NULL) *mb_cov_drain_in_flight = 0;
}
void g_cond_broadcast(GCond *condition) { (void)condition; }

int g_atomic_int_get(volatile gint *value) { return *value; }
void g_atomic_int_set(volatile gint *value, gint replacement) {
  if (mb_cov_fail_init && replacement == 1) return;
  *value = replacement;
}
void g_atomic_int_inc(volatile gint *value) { ++*value; }
void g_atomic_int_add(volatile gint *value, gint delta) { *value += delta; }
void g_assert(int expression) {
  if (!expression) abort();
}

void g_error_free(GError *error) {
  if (error == NULL) return;
  free(error->message);
  free(error);
}

GCancellable *g_cancellable_new(void) {
  if (mb_cov_fail_cancellable) { mb_cov_fail_cancellable = 0; return NULL; }
  return calloc(1, sizeof(GCancellable));
}
void g_cancellable_cancel(GCancellable *cancellable) {
  InterlockedExchange(&cancellable->cancelled, 1);
  InterlockedIncrement(&mb_cov_cancel_count);
}
void g_object_unref(void *object) { free(object); }

typedef struct {
  LPTHREAD_START_ROUTINE start;
  LPVOID parameter;
} mb_cov_thread_start;

static DWORD WINAPI mb_cov_thread_worker(LPVOID parameter) {
  mb_cov_thread_start *wrapper = parameter;
  LPTHREAD_START_ROUTINE start = wrapper->start;
  LPVOID data = wrapper->parameter;
  free(wrapper);
  DWORD result = start(data);
  InterlockedDecrement(&mb_cov_watchdogs_active);
  return result;
}

HANDLE mb_cov_CreateEventW(LPSECURITY_ATTRIBUTES attributes, BOOL manual_reset,
                           BOOL initial_state, LPCWSTR name) {
  if (mb_cov_fail_deadline_event) { mb_cov_fail_deadline_event = 0; SetLastError(ERROR_NOT_ENOUGH_MEMORY); return NULL; }
  return CreateEventW(attributes, manual_reset, initial_state, name);
}

HANDLE mb_cov_CreateThread(LPSECURITY_ATTRIBUTES attributes, SIZE_T stack_size,
                           LPTHREAD_START_ROUTINE start, LPVOID parameter,
                           DWORD flags, LPDWORD thread_id) {
  if (mb_cov_fail_deadline_thread) { mb_cov_fail_deadline_thread = 0; SetLastError(ERROR_NOT_ENOUGH_MEMORY); return NULL; }
  mb_cov_thread_start *wrapper = malloc(sizeof(*wrapper));
  if (wrapper == NULL) return NULL;
  wrapper->start = start;
  wrapper->parameter = parameter;
  InterlockedIncrement(&mb_cov_watchdogs_active);
  HANDLE thread = CreateThread(attributes, stack_size, mb_cov_thread_worker, wrapper, flags, thread_id);
  if (thread == NULL) { InterlockedDecrement(&mb_cov_watchdogs_active); free(wrapper); }
  return thread;
}

DWORD mb_cov_WaitForSingleObject(HANDLE handle, DWORD milliseconds) {
  if (mb_cov_force_deadline_timeout && milliseconds != INFINITE) {
    mb_cov_force_deadline_timeout = 0;
    return WAIT_TIMEOUT;
  }
  return WaitForSingleObject(handle, milliseconds);
}

void frida_init(void) {}
void frida_deinit(void) {}
FridaDeviceManager *frida_device_manager_new(void) { return &manager; }

FridaDevice *frida_device_manager_get_device_by_type_sync(
    FridaDeviceManager *value, FridaDeviceType type, int timeout,
    void *cancellable, GError **error) {
  (void)value;
  (void)type;
  (void)timeout;
  if (mb_cov_wait_for_cancel) {
    while (InterlockedCompareExchange(&((GCancellable *)cancellable)->cancelled, 0, 0) == 0) Sleep(1);
    mb_cov_wait_for_cancel = 0;
  }
  if (mb_cov_fail_device) {
    set_error(error, "device lookup failed");
    return NULL;
  }
  return &device;
}

void frida_device_manager_close_sync(FridaDeviceManager *value, void *cancellable, GError **error) {
  (void)value;
  (void)cancellable;
  (void)error;
}

FridaProcessQueryOptions *frida_process_query_options_new(void) {
  static FridaProcessQueryOptions options;
  return &options;
}
void frida_process_query_options_set_scope(FridaProcessQueryOptions *options, FridaScope scope) {
  (void)options;
  (void)scope;
}

static void init_processes(void) {
  if (processes_initialized) return;
  processes_initialized = 1;
  static const int ppid_kinds[] = {1, 2, 3, 4, 5, 6};
  static const int path_kinds[] = {1, 1, 1, 1, 0, 2};
  for (size_t i = 0; i < 6; i++) {
    processes[i].pid = (uint32_t)(100 + i);
    processes[i].name = i == 4 ? "fallback" : "fixture";
    processes[i].parameters.process = &processes[i];
    processes[i].ppid_kind = ppid_kinds[i];
    processes[i].path_kind = path_kinds[i];
    processes[i].ppid.type = ppid_kinds[i] == 1 ? 1 : ppid_kinds[i] == 2 ? 2 : ppid_kinds[i] == 3 ? 3 : ppid_kinds[i] == 4 ? 4 : 99;
    processes[i].ppid.signed_value = (int64_t)(200 + i);
    processes[i].ppid.unsigned_value = (uint64_t)(200 + i);
    processes[i].path.type = path_kinds[i] ? 5 : 99;
    processes[i].path.string_value = i == 0 ? "C:/fixture.exe" : "";
  }
  process_list.items = processes;
  process_list.size = 6;
}

FridaProcessList *frida_device_enumerate_processes_sync(
    FridaDevice *value, FridaProcessQueryOptions *options, void *cancellable, GError **error) {
  (void)value;
  (void)options;
  (void)cancellable;
  if (mb_cov_fail_enumerate) {
    set_error(error, "enumeration failed");
    return NULL;
  }
  init_processes();
  return &process_list;
}
int frida_process_list_size(FridaProcessList *list) { return (int)list->size; }
FridaProcess *frida_process_list_get(FridaProcessList *list, gint index) {
  return &list->items[index];
}
uint32_t frida_process_get_pid(FridaProcess *process) { return process->pid; }
const char *frida_process_get_name(FridaProcess *process) { return process->name; }
GHashTable *frida_process_get_parameters(FridaProcess *process) { return &process->parameters; }
void frida_unref(void *object) { (void)object; }

void *g_hash_table_lookup(GHashTable *table, const char *key) {
  if (table == NULL || table->process == NULL) return NULL;
  if (strcmp(key, "ppid") == 0) {
    if (table->process->ppid_kind == 5) return NULL;
    return &table->process->ppid;
  }
  if (strcmp(key, "path") == 0) {
    if (table->process->path_kind == 0) return NULL;
    return &table->process->path;
  }
  return NULL;
}

int g_variant_is_of_type(GVariant *value, void *type) {
  if (value == NULL) return 0;
  if (type == G_VARIANT_TYPE_INT32) return value->type == 1;
  if (type == G_VARIANT_TYPE_INT64) return value->type == 2;
  if (type == G_VARIANT_TYPE_UINT32) return value->type == 3;
  if (type == G_VARIANT_TYPE_UINT64) return value->type == 4;
  if (type == G_VARIANT_TYPE_STRING) return value->type == 5;
  return 0;
}
int32_t g_variant_get_int32(GVariant *value) { return (int32_t)value->signed_value; }
int64_t g_variant_get_int64(GVariant *value) { return value->signed_value; }
uint32_t g_variant_get_uint32(GVariant *value) { return (uint32_t)value->unsigned_value; }
uint64_t g_variant_get_uint64(GVariant *value) { return value->unsigned_value; }
const char *g_variant_get_string(GVariant *value, void *length) {
  (void)length;
  return value->string_value;
}

FridaSession *frida_device_attach_sync(
    FridaDevice *value, uint32_t pid, void *options, void *cancellable, GError **error) {
  (void)value;
  (void)pid;
  (void)options;
  (void)cancellable;
  if (mb_cov_fail_attach) {
    set_error(error, "attach failed");
    return NULL;
  }
  return &session;
}
int frida_session_detach_sync(FridaSession *value, void *cancellable, GError **error) {
  (void)value;
  (void)cancellable;
  if (mb_cov_fail_detach) {
    set_error(error, "detach failed");
    return 0;
  }
  return 1;
}
FridaScriptOptions *frida_script_options_new(void) {
  static FridaScriptOptions options;
  return &options;
}
FridaScript *frida_session_create_script_sync(
    FridaSession *value, const char *source, FridaScriptOptions *options,
    void *cancellable, GError **error) {
  (void)value;
  (void)source;
  (void)options;
  (void)cancellable;
  if (mb_cov_fail_create_script) {
    set_error(error, "script creation failed");
    return NULL;
  }
  return &script;
}
int frida_script_load_sync(FridaScript *value, void *cancellable, GError **error) {
  (void)value;
  (void)cancellable;
  if (mb_cov_fail_load_script) {
    set_error(error, "script load failed");
    return 0;
  }
  return 1;
}
int frida_script_unload_sync(FridaScript *value, void *cancellable, GError **error) {
  (void)value;
  (void)cancellable;
  if (mb_cov_fail_unload) {
    set_error(error, "script unload failed");
    return 0;
  }
  return 1;
}
void frida_script_post(FridaScript *value, const char *json, void *data) {
  (void)value;
  (void)json;
  (void)data;
}
const guint8 *g_bytes_get_data(GBytes *bytes, gsize *size) {
  if (size != NULL) *size = bytes != NULL ? bytes->size : 0;
  return bytes != NULL ? bytes->data : NULL;
}
void g_signal_connect_data(void *instance, const char *signal, void *callback,
                           void *user_data, void *destroy_data, int flags) {
  (void)instance;
  (void)destroy_data;
  (void)flags;
  if (strcmp(signal, "message") == 0) {
    mb_cov_message_callback = (mb_cov_message_handler)callback;
    mb_cov_message_user_data = user_data;
  } else if (strcmp(signal, "detached") == 0) {
    mb_cov_detached_callback = (mb_cov_detached_handler)callback;
    mb_cov_detached_user_data = user_data;
  }
}
void g_signal_handlers_disconnect_by_data(void *instance, void *user_data) {
  (void)instance;
  if (mb_cov_message_user_data == user_data) {
    mb_cov_message_callback = NULL;
    mb_cov_message_user_data = NULL;
  }
  if (mb_cov_detached_user_data == user_data) {
    mb_cov_detached_callback = NULL;
    mb_cov_detached_user_data = NULL;
  }
}

const char *zlibVersion(void) { return "1.3.1-coverage-double"; }
unsigned long compressBound(unsigned long source_length) {
  return source_length == 0 && mb_cov_decompress_mode == 9 ? 0 : source_length + 16;
}
int compress2(unsigned char *destination, unsigned long *destination_length,
              const unsigned char *source, unsigned long source_length, int level) {
  (void)level;
  if (mb_cov_fail_compress) return -2;
  if (*destination_length < source_length) return -5;
  if (source_length != 0) memcpy(destination, source, source_length);
  *destination_length = source_length;
  return 0;
}
int uncompress2(unsigned char *destination, unsigned long *destination_length,
                const unsigned char *source, unsigned long *source_length) {
  if (mb_cov_decompress_mode == 2) return -3;
  if (mb_cov_decompress_mode == 1 && mb_cov_decompress_calls++ == 0) return -5;
  if (mb_cov_decompress_mode == 3) {
    *destination_length = *source_length == 0 ? 1 : *source_length + 1;
    return 0;
  }
  if (*destination_length < *source_length) return -5;
  if (*source_length != 0) memcpy(destination, source, *source_length);
  *destination_length = *source_length;
  return 0;
}
