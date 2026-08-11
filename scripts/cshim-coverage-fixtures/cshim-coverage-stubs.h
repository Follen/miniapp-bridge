#ifndef MINIAPP_BRIDGE_CSHIM_COVERAGE_STUBS_H
#define MINIAPP_BRIDGE_CSHIM_COVERAGE_STUBS_H

/*
 * A small, deterministic Frida/GLib double used only by the gcov harness.
 * The production shim is compiled unchanged; this header supplies the
 * external SDK surface needed to exercise every error and cleanup path.
 */
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <windows.h>

typedef int gboolean;
typedef unsigned int guint;
typedef unsigned long gsize;
typedef int gint;
typedef unsigned char guint8;
typedef char gchar;
typedef void *gpointer;
typedef struct { int unused; } GMutex;
typedef struct { int unused; } GCond;
typedef struct _GError { char *message; } GError;
typedef struct _GCancellable { volatile LONG cancelled; } GCancellable;
typedef struct _GBytes { const guint8 *data; gsize size; } GBytes;
typedef struct _GVariant { int type; int64_t signed_value; uint64_t unsigned_value; const char *string_value; } GVariant;
typedef struct _GHashTable { struct FridaProcess *process; } GHashTable;

typedef struct FridaDeviceManager { int live; } FridaDeviceManager;
typedef struct FridaDevice { int live; } FridaDevice;
typedef struct FridaSession { int live; } FridaSession;
typedef struct FridaScript { int live; } FridaScript;
typedef struct FridaCrash { int unused; } FridaCrash;
typedef struct FridaScriptOptions { int unused; } FridaScriptOptions;
typedef struct FridaProcessQueryOptions { int unused; } FridaProcessQueryOptions;
typedef struct FridaProcess {
  uint32_t pid;
  const char *name;
  GHashTable parameters;
  GVariant ppid;
  GVariant path;
  int ppid_kind;
  int path_kind;
} FridaProcess;
typedef struct FridaProcessList { size_t size; FridaProcess *items; } FridaProcessList;

typedef int FridaDeviceType;
typedef int FridaScope;
typedef int FridaSessionDetachReason;

#define TRUE 1
#define FALSE 0
#define FRIDA_DEVICE_TYPE_LOCAL 0
#define FRIDA_SCOPE_METADATA 0
#define G_CALLBACK(value) (value)
#define G_VARIANT_TYPE_INT32 ((void *)1)
#define G_VARIANT_TYPE_INT64 ((void *)2)
#define G_VARIANT_TYPE_UINT32 ((void *)3)
#define G_VARIANT_TYPE_UINT64 ((void *)4)
#define G_VARIANT_TYPE_STRING ((void *)5)
#define _strdup mb_cov_strdup

/* Allocation hooks are enabled only for the shim translation unit. */
void *mb_cov_malloc(size_t size);
void *mb_cov_calloc(size_t count, size_t size);
void mb_cov_free(void *value);
char *mb_cov_strdup(const char *value);

extern int mb_cov_fail_init;
extern int mb_cov_fail_device;
extern int mb_cov_fail_enumerate;
extern int mb_cov_fail_attach;
extern int mb_cov_fail_create_script;
extern int mb_cov_fail_load_script;
extern int mb_cov_fail_detach;
extern int mb_cov_fail_unload;
extern int mb_cov_fail_compress;
extern int mb_cov_decompress_mode;
extern int mb_cov_decompress_calls;
extern int mb_cov_fail_malloc_once;
extern int mb_cov_fail_calloc_once;
extern guint *mb_cov_drain_in_flight;
extern int mb_cov_fail_cancellable;
extern int mb_cov_fail_deadline_event;
extern int mb_cov_fail_deadline_thread;
extern int mb_cov_force_deadline_timeout;
extern int mb_cov_wait_for_cancel;
extern volatile LONG mb_cov_cancel_count;
extern volatile LONG mb_cov_watchdogs_active;

typedef void (*mb_cov_message_handler)(FridaScript *, const gchar *, GBytes *, gpointer);
typedef void (*mb_cov_detached_handler)(FridaSession *, FridaSessionDetachReason, FridaCrash *, gpointer);
extern mb_cov_message_handler mb_cov_message_callback;
extern mb_cov_detached_handler mb_cov_detached_callback;
extern gpointer mb_cov_message_user_data;
extern gpointer mb_cov_detached_user_data;

void g_mutex_init(GMutex *mutex);
void g_mutex_clear(GMutex *mutex);
void g_mutex_lock(GMutex *mutex);
void g_mutex_unlock(GMutex *mutex);
void g_cond_init(GCond *condition);
void g_cond_clear(GCond *condition);
void g_cond_wait(GCond *condition, GMutex *mutex);
void g_cond_broadcast(GCond *condition);
int g_atomic_int_get(volatile gint *value);
void g_atomic_int_set(volatile gint *value, gint replacement);
void g_atomic_int_inc(volatile gint *value);
void g_atomic_int_add(volatile gint *value, gint delta);
void g_assert(int expression);
void g_error_free(GError *error);
GCancellable *g_cancellable_new(void);
void g_cancellable_cancel(GCancellable *cancellable);
void g_object_unref(void *object);

HANDLE mb_cov_CreateEventW(LPSECURITY_ATTRIBUTES attributes, BOOL manual_reset,
                           BOOL initial_state, LPCWSTR name);
HANDLE mb_cov_CreateThread(LPSECURITY_ATTRIBUTES attributes, SIZE_T stack_size,
                           LPTHREAD_START_ROUTINE start, LPVOID parameter,
                           DWORD flags, LPDWORD thread_id);
DWORD mb_cov_WaitForSingleObject(HANDLE handle, DWORD milliseconds);
#define CreateEventW mb_cov_CreateEventW
#define CreateThread mb_cov_CreateThread
#define WaitForSingleObject mb_cov_WaitForSingleObject

void frida_init(void);
void frida_deinit(void);
FridaDeviceManager *frida_device_manager_new(void);
FridaDevice *frida_device_manager_get_device_by_type_sync(
    FridaDeviceManager *manager, FridaDeviceType type, int timeout,
    void *cancellable, GError **error);
void frida_device_manager_close_sync(FridaDeviceManager *manager, void *cancellable, GError **error);
FridaProcessQueryOptions *frida_process_query_options_new(void);
void frida_process_query_options_set_scope(FridaProcessQueryOptions *options, FridaScope scope);
FridaProcessList *frida_device_enumerate_processes_sync(
    FridaDevice *device, FridaProcessQueryOptions *options, void *cancellable, GError **error);
int frida_process_list_size(FridaProcessList *list);
FridaProcess *frida_process_list_get(FridaProcessList *list, gint index);
uint32_t frida_process_get_pid(FridaProcess *process);
const char *frida_process_get_name(FridaProcess *process);
GHashTable *frida_process_get_parameters(FridaProcess *process);
void frida_unref(void *object);
void *g_hash_table_lookup(GHashTable *table, const char *key);
int g_variant_is_of_type(GVariant *value, void *type);
int32_t g_variant_get_int32(GVariant *value);
int64_t g_variant_get_int64(GVariant *value);
uint32_t g_variant_get_uint32(GVariant *value);
uint64_t g_variant_get_uint64(GVariant *value);
const char *g_variant_get_string(GVariant *value, void *length);
FridaSession *frida_device_attach_sync(
    FridaDevice *device, uint32_t pid, void *options, void *cancellable, GError **error);
int frida_session_detach_sync(FridaSession *session, void *cancellable, GError **error);
FridaScriptOptions *frida_script_options_new(void);
FridaScript *frida_session_create_script_sync(
    FridaSession *session, const char *source, FridaScriptOptions *options,
    void *cancellable, GError **error);
int frida_script_load_sync(FridaScript *script, void *cancellable, GError **error);
int frida_script_unload_sync(FridaScript *script, void *cancellable, GError **error);
void frida_script_post(FridaScript *script, const char *json, void *data);
const guint8 *g_bytes_get_data(GBytes *bytes, gsize *size);
void g_signal_connect_data(void *instance, const char *signal, void *callback,
                           void *user_data, void *destroy_data, int flags);
void g_signal_handlers_disconnect_by_data(void *instance, void *user_data);

const char *zlibVersion(void);
unsigned long compressBound(unsigned long source_length);
int compress2(unsigned char *destination, unsigned long *destination_length,
              const unsigned char *source, unsigned long source_length, int level);
int uncompress2(unsigned char *destination, unsigned long *destination_length,
                const unsigned char *source, unsigned long *source_length);

#endif
