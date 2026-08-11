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
static HANDLE g_guard = INVALID_HANDLE_VALUE;
static wchar_t *g_path;
static wchar_t *g_final_path;
static unsigned long g_refs;
static unsigned long g_device_leases;
static unsigned long g_session_leases;
static unsigned long g_script_leases;
static unsigned long g_process_leases;
static unsigned long g_active_calls;
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

typedef struct {
  DWORD volume;
  DWORD index_high;
  DWORD index_low;
} mb_file_identity;

static wchar_t *final_path_from_handle(HANDLE handle, char **error) {
  DWORD needed = GetFinalPathNameByHandleW(handle, NULL, 0, FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
  if (needed == 0) { set_win_error(error, "GetFinalPathNameByHandleW"); return NULL; }
  wchar_t *path = (wchar_t *)calloc((size_t)needed + 1, sizeof(wchar_t));
  if (path == NULL) { set_error(error, "native runtime final path allocation failed"); return NULL; }
  DWORD written = GetFinalPathNameByHandleW(handle, path, needed + 1, FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
  if (written == 0 || written > needed) { free(path); set_win_error(error, "GetFinalPathNameByHandleW"); return NULL; }
  return path;
}

static int inspect_runtime_file(HANDLE handle, mb_file_identity *identity, wchar_t **final_path, char **error) {
  BY_HANDLE_FILE_INFORMATION info;
  if (!GetFileInformationByHandle(handle, &info)) { set_win_error(error, "GetFileInformationByHandle"); return 0; }
  if ((info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) != 0) { set_error(error, "native runtime reparse points are not allowed"); return 0; }
  if ((info.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0) { set_error(error, "native runtime path is not a file"); return 0; }
  if (info.nNumberOfLinks != 1) { set_error(error, "native runtime must have exactly one hard link"); return 0; }
  wchar_t *resolved = final_path_from_handle(handle, error);
  if (resolved == NULL) return 0;
  identity->volume = info.dwVolumeSerialNumber;
  identity->index_high = info.nFileIndexHigh;
  identity->index_low = info.nFileIndexLow;
  *final_path = resolved;
  return 1;
}

static int same_file_identity(const mb_file_identity *left, const mb_file_identity *right) {
  return left->volume == right->volume && left->index_high == right->index_high && left->index_low == right->index_low;
}

static wchar_t *normalized_path_copy(const wchar_t *path) {
  if (path == NULL) return NULL;
  size_t length = wcslen(path);
  size_t prefix = 0;
  int unc = 0;
  /* Convert \\?\C:\... to C:\... and \\?\UNC\server\... to \\server\.... */
  if (length >= 8 && wcsncmp(path, L"\\\\?\\UNC\\", 8) == 0) {
    prefix = 8;
    unc = 1;
  } else if (length >= 4 && wcsncmp(path, L"\\\\?\\", 4) == 0) {
    prefix = 4;
  }
  size_t output_length = length - prefix + (unc ? 2 : 0);
  wchar_t *normalized = (wchar_t *)calloc(output_length + 1, sizeof(wchar_t));
  if (normalized == NULL) return NULL;
  if (unc) {
    normalized[0] = L'\\';
    normalized[1] = L'\\';
    memcpy(normalized + 2, path + prefix, (length - prefix) * sizeof(wchar_t));
  } else {
    memcpy(normalized, path + prefix, (length - prefix) * sizeof(wchar_t));
  }
  return normalized;
}

static int same_normalized_path(const wchar_t *left, const wchar_t *right) {
  if (left == NULL || right == NULL) return left == right;
  wchar_t *normalized_left = normalized_path_copy(left);
  wchar_t *normalized_right = normalized_path_copy(right);
  int same = normalized_left != NULL && normalized_right != NULL && _wcsicmp(normalized_left, normalized_right) == 0;
  free(normalized_left);
  free(normalized_right);
  return same;
}

static HANDLE open_runtime_guard(const wchar_t *path, mb_file_identity *identity, wchar_t **final_path, char **error) {
  HANDLE handle = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING,
      FILE_ATTRIBUTE_NORMAL | FILE_FLAG_OPEN_REPARSE_POINT, NULL);
  if (handle == INVALID_HANDLE_VALUE) {
    /* Keep the historical diagnostic prefix used by callers for missing and
       unloadable native runtimes. */
    set_win_error(error, "LoadLibraryExW");
    return INVALID_HANDLE_VALUE;
  }
  if (!inspect_runtime_file(handle, identity, final_path, error)) { CloseHandle(handle); return INVALID_HANDLE_VALUE; }
  return handle;
}

static int verify_loaded_identity(HMODULE module, const mb_file_identity *expected, const wchar_t *expected_path, char **error) {
  wchar_t module_path[32768];
  DWORD length = GetModuleFileNameW(module, module_path, (DWORD)(sizeof(module_path) / sizeof(module_path[0])));
  if (length == 0 || length == (DWORD)(sizeof(module_path) / sizeof(module_path[0]))) {
    set_win_error(error, "GetModuleFileNameW");
    return 0;
  }
  HANDLE loaded = CreateFileW(module_path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING,
      FILE_ATTRIBUTE_NORMAL | FILE_FLAG_OPEN_REPARSE_POINT, NULL);
  if (loaded == INVALID_HANDLE_VALUE) { set_win_error(error, "open loaded native runtime"); return 0; }
  mb_file_identity actual;
  wchar_t *actual_path = NULL;
  int ok = inspect_runtime_file(loaded, &actual, &actual_path, error);
  CloseHandle(loaded);
  if (ok && (!same_file_identity(expected, &actual) || !same_normalized_path(expected_path, actual_path))) {
    set_error(error, "loaded native runtime identity changed");
    ok = 0;
  }
  free(actual_path);
  return ok;
}

static void clear_functions(void) {
  p_abi_version = NULL; p_native_version = NULL; p_frida_core_version = NULL; p_zlib_version = NULL;
  p_zlib_compress = NULL; p_zlib_decompress = NULL; p_bytes_free = NULL;
  p_device_open = NULL; p_device_enumerate = NULL; p_processes_free = NULL;
  p_device_attach = NULL; p_device_close = NULL; p_runtime_shutdown = NULL;
  p_session_load_script = NULL; p_session_detach = NULL; p_script_post = NULL;
  p_script_unload = NULL; p_error_free = NULL;
}

enum mb_object_kind { MB_OBJECT_DEVICE, MB_OBJECT_SESSION, MB_OBJECT_SCRIPT, MB_OBJECT_PROCESS };

static void object_retain_locked(enum mb_object_kind kind) {
  if (kind == MB_OBJECT_DEVICE) g_device_leases++;
  else if (kind == MB_OBJECT_SESSION) g_session_leases++;
  else if (kind == MB_OBJECT_SCRIPT) g_script_leases++;
  else g_process_leases++;
}

static void object_release_locked(enum mb_object_kind kind) {
  unsigned long *count = kind == MB_OBJECT_DEVICE ? &g_device_leases :
      (kind == MB_OBJECT_SESSION ? &g_session_leases :
      (kind == MB_OBJECT_SCRIPT ? &g_script_leases : &g_process_leases));
  if (*count > 0) (*count)--;
}

static void unload_if_idle_locked(void) {
  if (g_module == NULL || g_refs != 0 || g_device_leases != 0 || g_session_leases != 0 ||
      g_script_leases != 0 || g_process_leases != 0 || g_active_calls != 0) return;
  HMODULE module = g_module;
  HANDLE guard = g_guard;
  g_module = NULL;
  g_guard = INVALID_HANDLE_VALUE;
  free(g_path); g_path = NULL;
  free(g_final_path); g_final_path = NULL;
  /* p_runtime_shutdown belongs to module and is called only after every
     object, callback owner, and active exported call has released its lease. */
  if (p_runtime_shutdown != NULL) p_runtime_shutdown();
  clear_functions();
  FreeLibrary(module);
  if (guard != INVALID_HANDLE_VALUE) CloseHandle(guard);
}

static int begin_call(void) {
  int active = 0;
  ensure_lock();
  EnterCriticalSection(&g_lock);
  if (g_module != NULL) { g_active_calls++; active = 1; }
  LeaveCriticalSection(&g_lock);
  return active;
}

static void finish_call(void) {
  ensure_lock();
  EnterCriticalSection(&g_lock);
  if (g_active_calls > 0) g_active_calls--;
  unload_if_idle_locked();
  LeaveCriticalSection(&g_lock);
}

static void finish_call_created(enum mb_object_kind kind, int created) {
  ensure_lock();
  EnterCriticalSection(&g_lock);
  if (created) object_retain_locked(kind);
  if (g_active_calls > 0) g_active_calls--;
  unload_if_idle_locked();
  LeaveCriticalSection(&g_lock);
}

static void finish_call_released(enum mb_object_kind kind) {
  ensure_lock();
  EnterCriticalSection(&g_lock);
  object_release_locked(kind);
  if (g_active_calls > 0) g_active_calls--;
  unload_if_idle_locked();
  LeaveCriticalSection(&g_lock);
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
  mb_file_identity identity;
  wchar_t *final_path = NULL;
  HANDLE guard = open_runtime_guard(path, &identity, &final_path, error);
  if (guard == INVALID_HANDLE_VALUE) {
    if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD;
    LeaveCriticalSection(&g_lock);
    return 0;
  }
  HMODULE module = LoadLibraryExW(path, NULL, LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32);
  if (module == NULL) { set_win_error(error, "LoadLibraryExW"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD; goto load_fail; }
  if (!verify_loaded_identity(module, &identity, final_path, error)) {
    if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD;
    goto load_fail;
  }
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
    set_error(error, "native runtime version exports returned NULL"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; goto load_fail;
  }
  if (p_abi_version() != MB_ABI_VERSION) {
    set_error(error, "native runtime ABI version mismatch"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_ABI; goto load_fail;
  }
  if (strcmp(p_native_version(), MB_NATIVE_VERSION) != 0) {
    set_error(error, "native runtime version mismatch"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; goto load_fail;
  }
  if (strcmp(p_frida_core_version(), MB_FRIDA_CORE_VERSION) != 0) {
    set_error(error, "frida-core version mismatch"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; goto load_fail;
  }
  if (strcmp(p_zlib_version(), MB_ZLIB_VERSION) != 0) {
    char mismatch[160]; snprintf(mismatch, sizeof(mismatch), "zlib version mismatch: expected=%s actual=%s", MB_ZLIB_VERSION, p_zlib_version());
    set_error(error, mismatch); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_VERSION; goto load_fail;
  }
  size_t bytes = (wcslen(path) + 1) * sizeof(wchar_t);
  g_path = (wchar_t *)malloc(bytes);
  if (g_path == NULL) { set_error(error, "native runtime path allocation failed"); if (load_code != NULL) *load_code = MB_NATIVE_LOAD_ERROR_LOAD; goto load_fail; }
  memcpy(g_path, path, bytes);
  g_module = module; g_guard = guard; guard = INVALID_HANDLE_VALUE; g_final_path = final_path; final_path = NULL;
  g_refs = 1; g_device_leases = 0; g_session_leases = 0; g_script_leases = 0; g_process_leases = 0; g_active_calls = 0;
  LeaveCriticalSection(&g_lock);
  return 1;

resolve_fail:
  if (load_code != NULL && *load_code == MB_NATIVE_LOAD_OK) *load_code = MB_NATIVE_LOAD_ERROR_EXPORT;
load_fail:
  FreeLibrary(module);
  clear_functions();
  if (guard != INVALID_HANDLE_VALUE) CloseHandle(guard);
  free(final_path);
  free(g_path); g_path = NULL;
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
  unload_if_idle_locked();
  LeaveCriticalSection(&g_lock);
}

int mb_native_loaded(void) { int loaded; ensure_lock(); EnterCriticalSection(&g_lock); loaded = g_module != NULL ? 1 : 0; LeaveCriticalSection(&g_lock); return loaded; }
uint32_t mb_abi_version(void) { int loaded; ensure_lock(); EnterCriticalSection(&g_lock); loaded = g_module != NULL; LeaveCriticalSection(&g_lock); return loaded ? MB_ABI_VERSION : 0; }
const char *mb_native_version(void) { int loaded; ensure_lock(); EnterCriticalSection(&g_lock); loaded = g_module != NULL; LeaveCriticalSection(&g_lock); return loaded ? MB_NATIVE_VERSION : ""; }
const char *mb_frida_core_version(void) { int loaded; ensure_lock(); EnterCriticalSection(&g_lock); loaded = g_module != NULL; LeaveCriticalSection(&g_lock); return loaded ? MB_FRIDA_CORE_VERSION : ""; }
const char *mb_zlib_version(void) { int loaded; ensure_lock(); EnterCriticalSection(&g_lock); loaded = g_module != NULL; LeaveCriticalSection(&g_lock); return loaded ? MB_ZLIB_VERSION : ""; }
static void copy_native_error(char **out, char *native_error) {
  if (native_error == NULL) return;
  if (out != NULL) *out = _strdup(native_error);
  if (p_error_free != NULL) p_error_free(native_error);
}
int mb_zlib_compress(const uint8_t *i, size_t n, uint8_t **o, size_t *s, char **e) {
  if (o == NULL || s == NULL || !begin_call()) return 0;
  char *ne = NULL; uint8_t *native_output = NULL; size_t native_size = 0;
  int ok = p_zlib_compress(i,n,&native_output,&native_size,&ne);
  copy_native_error(e,ne);
  if (ok) {
    if (native_size > MB_MAX_ZLIB_OUTPUT) {
      if (p_bytes_free != NULL) p_bytes_free(native_output);
      set_error(e, "native zlib output exceeds configured limit");
      ok = 0;
    } else {
      uint8_t *copy = (uint8_t *)malloc(native_size != 0 ? native_size : 1);
      if (copy == NULL) { if (p_bytes_free != NULL) p_bytes_free(native_output); set_error(e, "native zlib output allocation failed"); ok = 0; }
      else { if (native_size != 0) memcpy(copy, native_output, native_size); if (p_bytes_free != NULL) p_bytes_free(native_output); *o = copy; *s = native_size; }
    }
  }
  finish_call(); return ok;
}
int mb_zlib_decompress(const uint8_t *i, size_t n, size_t x, size_t m, uint8_t **o, size_t *s, char **e) {
  if (o == NULL || s == NULL || !begin_call()) return 0;
  char *ne = NULL; uint8_t *native_output = NULL; size_t native_size = 0;
  int ok = p_zlib_decompress(i,n,x,m,&native_output,&native_size,&ne);
  copy_native_error(e,ne);
  if (ok) {
    if (native_size > MB_MAX_ZLIB_OUTPUT || (m != 0 && native_size > m)) {
      if (p_bytes_free != NULL) p_bytes_free(native_output);
      set_error(e, "native zlib output exceeds configured limit");
      ok = 0;
    } else {
      uint8_t *copy = (uint8_t *)malloc(native_size != 0 ? native_size : 1);
      if (copy == NULL) { if (p_bytes_free != NULL) p_bytes_free(native_output); set_error(e, "native zlib output allocation failed"); ok = 0; }
      else { if (native_size != 0) memcpy(copy, native_output, native_size); if (p_bytes_free != NULL) p_bytes_free(native_output); *o = copy; *s = native_size; }
    }
  }
  finish_call(); return ok;
}
void mb_bytes_free(uint8_t *b) { free(b); }
mb_device *mb_device_open(char **error) { if (!begin_call()) return NULL; char *ne = NULL; mb_device *result = p_device_open(&ne); copy_native_error(error,ne); finish_call_created(MB_OBJECT_DEVICE, result != NULL); return result; }
int mb_device_enumerate(mb_device *d, mb_process **i, size_t *c, char **e) { if (!begin_call()) return 0; char *ne = NULL; int ok = p_device_enumerate(d, i, c, &ne); copy_native_error(e,ne); finish_call_created(MB_OBJECT_PROCESS, ok && i != NULL && *i != NULL); return ok; }
void mb_processes_free(mb_process *i, size_t c) { if (!i || !begin_call()) return; p_processes_free(i, c); finish_call_released(MB_OBJECT_PROCESS); }
mb_session *mb_device_attach(mb_device *d, uint32_t p, uintptr_t h, mb_detached_cb cb, char **e) { if (!begin_call()) return NULL; char *ne = NULL; mb_session *result = p_device_attach(d, p, h, cb, &ne); copy_native_error(e,ne); finish_call_created(MB_OBJECT_SESSION, result != NULL); return result; }
void mb_device_close(mb_device *d) { if (!d || !begin_call()) return; p_device_close(d); finish_call_released(MB_OBJECT_DEVICE); }
void mb_runtime_shutdown(void) {
  fn_runtime_shutdown shutdown = NULL;
  ensure_lock(); EnterCriticalSection(&g_lock);
  if (g_module != NULL && g_refs != 0 && g_device_leases == 0 && g_session_leases == 0 && g_script_leases == 0 && g_process_leases == 0 && g_active_calls == 0) { shutdown = p_runtime_shutdown; g_active_calls++; }
  LeaveCriticalSection(&g_lock);
  if (shutdown != NULL) { shutdown(); finish_call(); }
}
mb_script *mb_session_load_script(mb_session *s, const char *src, uintptr_t h, mb_message_cb cb, char **e) { if (!begin_call()) return NULL; char *ne = NULL; mb_script *result = p_session_load_script(s, src, h, cb, &ne); copy_native_error(e,ne); finish_call_created(MB_OBJECT_SCRIPT, result != NULL); return result; }
int mb_session_detach(mb_session *s, char **e) { if (!s || !begin_call()) return 0; char *ne = NULL; int ok = p_session_detach(s, &ne); copy_native_error(e,ne); finish_call_released(MB_OBJECT_SESSION); return ok; }
int mb_script_post(mb_script *s, const char *j, char **e) { if (!s || !begin_call()) return 0; char *ne = NULL; int ok = p_script_post(s, j, &ne); copy_native_error(e,ne); finish_call(); return ok; }
int mb_script_unload(mb_script *s, char **e) { if (!s || !begin_call()) return 0; char *ne = NULL; int ok = p_script_unload(s, &ne); copy_native_error(e,ne); finish_call_released(MB_OBJECT_SCRIPT); return ok; }
void mb_error_free(char *e) { free(e); }
