#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

#include "loader_windows.h"

typedef uint32_t (*fn_abi_version)(void);
typedef const char *(*fn_string)(void);
typedef int (*fn_zlib_compress)(const uint8_t *, size_t, uint8_t **, size_t *, char **);
typedef int (*fn_zlib_decompress)(const uint8_t *, size_t, size_t, size_t, uint8_t **, size_t *, char **);
typedef void (*fn_bytes_free)(uint8_t *);
typedef mb_device *(*fn_device_open)(char **);
typedef int (*fn_device_enumerate)(mb_device *, mb_process **, size_t *, char **);
typedef void (*fn_processes_free)(mb_process *, size_t);
typedef mb_session *(*fn_device_attach)(mb_device *, uint32_t, uintptr_t, mb_detached_cb, char **);
typedef void (*fn_device_close)(mb_device *);
typedef void (*fn_runtime_shutdown)(void);
typedef mb_script *(*fn_session_load_script)(mb_session *, const char *, uintptr_t, mb_message_cb, char **);
typedef int (*fn_session_detach)(mb_session *, char **);
typedef int (*fn_script_post)(mb_script *, const char *, char **);
typedef int (*fn_script_unload)(mb_script *, char **);
typedef void (*fn_error_free)(char *);

static HMODULE g_module;
static wchar_t *g_path;
static unsigned long g_refs;
static CRITICAL_SECTION g_lock;
static volatile LONG g_lock_ready;

static fn_abi_version p_abi_version;
static fn_string p_native_version;
static fn_string p_frida_core_version;
static fn_string p_zlib_version;
static fn_zlib_compress p_zlib_compress;
static fn_zlib_decompress p_zlib_decompress;
static fn_bytes_free p_bytes_free;
static fn_device_open p_device_open;
static fn_device_enumerate p_device_enumerate;
static fn_processes_free p_processes_free;
static fn_device_attach p_device_attach;
static fn_device_close p_device_close;
static fn_runtime_shutdown p_runtime_shutdown;
static fn_session_load_script p_session_load_script;
static fn_session_detach p_session_detach;
static fn_script_post p_script_post;
static fn_script_unload p_script_unload;
static fn_error_free p_error_free;

static void ensure_lock(void) {
  if (InterlockedCompareExchange(&g_lock_ready, 1, 0) == 0) {
    InitializeCriticalSection(&g_lock);
    InterlockedExchange(&g_lock_ready, 2);
  }
  while (g_lock_ready != 2) SwitchToThread();
}

static void set_error(char **out, const char *message) {
  if (out != NULL) *out = _strdup(message != NULL ? message : "native runtime error");
}

static void set_win_error(char **out, const char *operation) {
  DWORD code = GetLastError();
  char detail[192] = {0};
  char message[320];
  DWORD length = FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS,
      NULL, code, MAKELANGID(LANG_NEUTRAL, SUBLANG_DEFAULT), detail, sizeof(detail), NULL);
  while (length > 0 && (detail[length - 1] == '\r' || detail[length - 1] == '\n')) detail[--length] = '\0';
  if (length > 0) snprintf(message, sizeof(message), "%s failed (win32=%lu): %s", operation, (unsigned long)code, detail);
  else snprintf(message, sizeof(message), "%s failed (win32=%lu)", operation, (unsigned long)code);
  set_error(out, message);
}

static void clear_functions(void) {
  p_abi_version = NULL; p_native_version = NULL; p_frida_core_version = NULL; p_zlib_version = NULL;
  p_zlib_compress = NULL; p_zlib_decompress = NULL; p_bytes_free = NULL;
  p_device_open = NULL; p_device_enumerate = NULL; p_processes_free = NULL;
  p_device_attach = NULL; p_device_close = NULL; p_runtime_shutdown = NULL;
  p_session_load_script = NULL; p_session_detach = NULL; p_script_post = NULL;
  p_script_unload = NULL; p_error_free = NULL;
}

#define RESOLVE(name, type) do { \
  p_##name = (type)GetProcAddress(module, "mb_" #name); \
  if (p_##name == NULL) { char missing[96]; snprintf(missing, sizeof(missing), "missing export: mb_%s", #name); set_error(error, missing); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_EXPORT; goto resolve_fail; } \
} while (0)

int mb_native_load(const wchar_t *path, char **error, int *load_code) {
  if (load_code != NULL) *load_code = MB_NATIVE_LOAD_OK;
  if (path == NULL || path[0] == L'\0') { set_error(error, "native runtime path is empty"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD; return 0; }
  ensure_lock();
  EnterCriticalSection(&g_lock);
  if (g_module != NULL) {
    if (g_path != NULL && _wcsicmp(g_path, path) == 0) { g_refs++; LeaveCriticalSection(&g_lock); return 1; }
    set_error(error, "a different native runtime is already loaded");
    if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_CONFLICT;
    LeaveCriticalSection(&g_lock);
    return 0;
  }
  HMODULE module = LoadLibraryExW(path, NULL, LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32);
  if (module == NULL) { set_win_error(error, "LoadLibraryExW"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD; LeaveCriticalSection(&g_lock); return 0; }
  RESOLVE(abi_version, fn_abi_version);
  RESOLVE(native_version, fn_string);
  RESOLVE(frida_core_version, fn_string);
  RESOLVE(zlib_version, fn_string);
  RESOLVE(zlib_compress, fn_zlib_compress);
  RESOLVE(zlib_decompress, fn_zlib_decompress);
  RESOLVE(bytes_free, fn_bytes_free);
  RESOLVE(device_open, fn_device_open);
  RESOLVE(device_enumerate, fn_device_enumerate);
  RESOLVE(processes_free, fn_processes_free);
  RESOLVE(device_attach, fn_device_attach);
  RESOLVE(device_close, fn_device_close);
  RESOLVE(runtime_shutdown, fn_runtime_shutdown);
  RESOLVE(session_load_script, fn_session_load_script);
  RESOLVE(session_detach, fn_session_detach);
  RESOLVE(script_post, fn_script_post);
  RESOLVE(script_unload, fn_script_unload);
  RESOLVE(error_free, fn_error_free);
  if (p_abi_version() == 0 || p_native_version() == NULL || p_frida_core_version() == NULL || p_zlib_version() == NULL) {
    set_error(error, "native runtime version exports returned NULL"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; FreeLibrary(module); clear_functions(); LeaveCriticalSection(&g_lock); return 0;
  }
  if (p_abi_version() != MB_ABI_VERSION) {
    set_error(error, "native runtime ABI version mismatch"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_ABI; FreeLibrary(module); clear_functions(); LeaveCriticalSection(&g_lock); return 0;
  }
  if (strcmp(p_native_version(), MB_NATIVE_VERSION) != 0) {
    set_error(error, "native runtime version mismatch"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; FreeLibrary(module); clear_functions(); LeaveCriticalSection(&g_lock); return 0;
  }
  if (strcmp(p_frida_core_version(), MB_FRIDA_CORE_VERSION) != 0) {
    set_error(error, "frida-core version mismatch"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; FreeLibrary(module); clear_functions(); LeaveCriticalSection(&g_lock); return 0;
  }
  if (strcmp(p_zlib_version(), MB_ZLIB_VERSION) != 0) {
    char mismatch[160]; snprintf(mismatch, sizeof(mismatch), "zlib version mismatch: expected=%s actual=%s", MB_ZLIB_VERSION, p_zlib_version());
    set_error(error, mismatch); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; FreeLibrary(module); clear_functions(); LeaveCriticalSection(&g_lock); return 0;
  }
  size_t bytes = (wcslen(path) + 1) * sizeof(wchar_t);
  g_path = (wchar_t *)malloc(bytes);
  if (g_path == NULL) { set_error(error, "native runtime path allocation failed"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD; FreeLibrary(module); clear_functions(); LeaveCriticalSection(&g_lock); return 0; }
  memcpy(g_path, path, bytes);
  g_module = module; g_refs = 1;
  LeaveCriticalSection(&g_lock);
  return 1;

resolve_fail:
  FreeLibrary(module);
  clear_functions();
  LeaveCriticalSection(&g_lock);
  return 0;
}

int mb_native_retain_loaded(void) {
  int retained = 0;
  ensure_lock(); EnterCriticalSection(&g_lock);
  if (g_module != NULL) { g_refs++; retained = 1; }
  LeaveCriticalSection(&g_lock); return retained;
}

void mb_native_release(void) {
  ensure_lock();
  EnterCriticalSection(&g_lock);
  if (g_refs > 0) g_refs--;
  if (g_refs == 0 && g_module != NULL) {
    HMODULE module = g_module; g_module = NULL;
    free(g_path); g_path = NULL;
    /* The shutdown export belongs to module. Call it after the runtime's last
       owner has released its Frida objects, but before clearing exports and
       unloading the DLL. */
    if (p_runtime_shutdown != NULL) p_runtime_shutdown();
    clear_functions(); FreeLibrary(module);
  }
  LeaveCriticalSection(&g_lock);
}

int mb_native_loaded(void) { int loaded; ensure_lock(); EnterCriticalSection(&g_lock); loaded = g_module != NULL ? 1 : 0; LeaveCriticalSection(&g_lock); return loaded; }
uint32_t mb_abi_version(void) { return p_abi_version != NULL ? p_abi_version() : 0; }
const char *mb_native_version(void) { return p_native_version != NULL ? p_native_version() : ""; }
const char *mb_frida_core_version(void) { return p_frida_core_version != NULL ? p_frida_core_version() : ""; }
const char *mb_zlib_version(void) { return p_zlib_version != NULL ? p_zlib_version() : ""; }
static void copy_native_error(char **out, char *native_error) {
  if (native_error == NULL) return;
  if (out != NULL) *out = _strdup(native_error);
  if (p_error_free != NULL) p_error_free(native_error);
}
int mb_zlib_compress(const uint8_t *i, size_t n, uint8_t **o, size_t *s, char **e) { char *ne = NULL; int ok = p_zlib_compress != NULL ? p_zlib_compress(i,n,o,s,&ne) : 0; copy_native_error(e,ne); return ok; }
int mb_zlib_decompress(const uint8_t *i, size_t n, size_t x, size_t m, uint8_t **o, size_t *s, char **e) { char *ne = NULL; int ok = p_zlib_decompress != NULL ? p_zlib_decompress(i,n,x,m,o,s,&ne) : 0; copy_native_error(e,ne); return ok; }
void mb_bytes_free(uint8_t *b) { if (p_bytes_free != NULL) p_bytes_free(b); }
mb_device *mb_device_open(char **error) { char *ne = NULL; mb_device *result = p_device_open != NULL ? p_device_open(&ne) : NULL; copy_native_error(error,ne); return result; }
int mb_device_enumerate(mb_device *d, mb_process **i, size_t *c, char **e) { char *ne = NULL; int ok = p_device_enumerate != NULL ? p_device_enumerate(d, i, c, &ne) : 0; copy_native_error(e,ne); return ok; }
void mb_processes_free(mb_process *i, size_t c) { if (p_processes_free != NULL) p_processes_free(i, c); }
mb_session *mb_device_attach(mb_device *d, uint32_t p, uintptr_t h, mb_detached_cb cb, char **e) { char *ne = NULL; mb_session *result = p_device_attach != NULL ? p_device_attach(d, p, h, cb, &ne) : NULL; copy_native_error(e,ne); return result; }
void mb_device_close(mb_device *d) { if (p_device_close != NULL) p_device_close(d); }
void mb_runtime_shutdown(void) { if (p_runtime_shutdown != NULL) p_runtime_shutdown(); }
mb_script *mb_session_load_script(mb_session *s, const char *src, uintptr_t h, mb_message_cb cb, char **e) { char *ne = NULL; mb_script *result = p_session_load_script != NULL ? p_session_load_script(s, src, h, cb, &ne) : NULL; copy_native_error(e,ne); return result; }
int mb_session_detach(mb_session *s, char **e) { char *ne = NULL; int ok = p_session_detach != NULL ? p_session_detach(s, &ne) : 0; copy_native_error(e,ne); return ok; }
int mb_script_post(mb_script *s, const char *j, char **e) { char *ne = NULL; int ok = p_script_post != NULL ? p_script_post(s, j, &ne) : 0; copy_native_error(e,ne); return ok; }
int mb_script_unload(mb_script *s, char **e) { char *ne = NULL; int ok = p_script_unload != NULL ? p_script_unload(s, &ne) : 0; copy_native_error(e,ne); return ok; }
void mb_error_free(char *e) { free(e); }
