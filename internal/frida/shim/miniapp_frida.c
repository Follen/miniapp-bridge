#include "miniapp_frida.h"
#include "frida-core.h"
#include <stdlib.h>
#include <string.h>

struct mb_device { FridaDeviceManager *manager; FridaDevice *device; };
struct mb_session { FridaSession *session; uintptr_t handle; mb_detached_cb detached; };
struct mb_script { FridaScript *script; uintptr_t handle; mb_message_cb message; };
static volatile gint mb_frida_refs = 0;
static volatile gint mb_frida_initialized = 0;

static void mb_frida_release(void) {
  if (g_atomic_int_get(&mb_frida_refs) > 0) g_atomic_int_add(&mb_frida_refs, -1);
}

static void mb_set_error(char **out, GError *error) {
  if (out != NULL) *out = _strdup(error != NULL ? error->message : "frida operation failed");
  if (error != NULL) g_error_free(error);
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
  mb_script *owner = user_data; gsize size = 0;
  const guint8 *bytes = data != NULL ? g_bytes_get_data(data, &size) : NULL;
  if (owner->message != NULL) owner->message(owner->handle, (char *) message, (uint8_t *) bytes, size);
}
static void mb_on_detached(FridaSession *session, FridaSessionDetachReason reason, FridaCrash *crash, gpointer user_data) {
  mb_session *owner = user_data; (void) session; (void) crash;
  if (owner->detached != NULL) owner->detached(owner->handle, (int) reason);
}

mb_device *mb_device_open(char **error) {
  GError *native_error = NULL;
  if (g_atomic_int_get(&mb_frida_initialized) == 0) {
    frida_init();
    g_atomic_int_set(&mb_frida_initialized, 1);
  }
  g_atomic_int_inc(&mb_frida_refs);
  mb_device *owner = calloc(1, sizeof(mb_device));
  if (owner == NULL) { if (error != NULL) *error = _strdup("out of memory"); mb_frida_release(); return NULL; }
  owner->manager = frida_device_manager_new();
  owner->device = frida_device_manager_get_device_by_type_sync(owner->manager, FRIDA_DEVICE_TYPE_LOCAL, -1, NULL, &native_error);
  if (owner->device == NULL) { mb_set_error(error, native_error); mb_device_close(owner); return NULL; }
  return owner;
}
int mb_device_enumerate(mb_device *device, mb_process **items, size_t *count, char **error) {
  GError *native_error = NULL; FridaProcessQueryOptions *options = frida_process_query_options_new();
  frida_process_query_options_set_scope(options, FRIDA_SCOPE_METADATA);
  FridaProcessList *list = frida_device_enumerate_processes_sync(device->device, options, NULL, &native_error);
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
  GError *native_error = NULL; FridaSession *session = frida_device_attach_sync(device->device, pid, NULL, NULL, &native_error);
  if (session == NULL) { mb_set_error(error, native_error); return NULL; }
  mb_session *owner = calloc(1, sizeof(mb_session)); owner->session=session; owner->handle=handle; owner->detached=callback;
  g_signal_connect_data(session, "detached", G_CALLBACK(mb_on_detached), owner, NULL, 0); return owner;
}
void mb_device_close(mb_device *device) { if(device==NULL)return; if(device->device)frida_unref(device->device); if(device->manager){frida_device_manager_close_sync(device->manager,NULL,NULL);frida_unref(device->manager);} free(device); mb_frida_release(); }
void mb_runtime_shutdown(void) { if(g_atomic_int_get(&mb_frida_refs)!=0)return; if(g_atomic_int_compare_and_exchange(&mb_frida_initialized,1,0))frida_deinit(); }
mb_script *mb_session_load_script(mb_session *session, const char *source, uintptr_t handle, mb_message_cb callback, char **error) {
  GError *native_error=NULL; FridaScriptOptions *options=frida_script_options_new();
  FridaScript *script=frida_session_create_script_sync(session->session,source,options,NULL,&native_error); frida_unref(options);
  if(script==NULL){mb_set_error(error,native_error);return NULL;}
  mb_script *owner=calloc(1,sizeof(mb_script)); owner->script=script;owner->handle=handle;owner->message=callback;
  g_signal_connect_data(script,"message",G_CALLBACK(mb_on_message),owner,NULL,0); frida_script_load_sync(script,NULL,&native_error);
  if(native_error!=NULL){mb_set_error(error,native_error);g_signal_handlers_disconnect_by_data(script,owner);frida_unref(script);free(owner);return NULL;} return owner;
}
int mb_session_detach(mb_session *session, char **error) { if(session==NULL)return 1; GError *e=NULL; g_signal_handlers_disconnect_by_data(session->session,session);frida_session_detach_sync(session->session,NULL,&e);frida_unref(session->session);free(session);if(e){mb_set_error(error,e);return 0;}return 1; }
int mb_script_post(mb_script *script, const char *json, char **error) { (void)error; if(script==NULL)return 0;frida_script_post(script->script,json,NULL);return 1; }
int mb_script_unload(mb_script *script, char **error) { if(script==NULL)return 1;GError *e=NULL;g_signal_handlers_disconnect_by_data(script->script,script);frida_script_unload_sync(script->script,NULL,&e);frida_unref(script->script);free(script);if(e){mb_set_error(error,e);return 0;}return 1; }
void mb_error_free(char *error) { free(error); }
