#ifndef MINIAPP_FRIDA_LOADER_WINDOWS_H
#define MINIAPP_FRIDA_LOADER_WINDOWS_H

#include <stddef.h>
#include <stdint.h>
#include <wchar.h>

typedef struct mb_device mb_device;
typedef struct mb_session mb_session;
typedef struct mb_script mb_script;

typedef struct {
  uint32_t pid;
  uint32_t ppid;
  char *name;
  char *path;
} mb_process;

typedef void (*mb_message_cb)(uintptr_t handle, char *message, uint8_t *data, size_t size);
typedef void (*mb_detached_cb)(uintptr_t handle, int reason);

#define MB_ABI_VERSION 1u
#define MB_NATIVE_VERSION "17.3.2-abi1.1"
#define MB_FRIDA_CORE_VERSION "17.3.2"
#define MB_ZLIB_VERSION "1.3.1"
#define MB_MAX_ZLIB_OUTPUT ((size_t)(256u * 1024u * 1024u))

#define MB_NATIVE_LOAD_OK 0
#define MB_NATIVE_LOAD_ERROR_LOAD 1
#define MB_NATIVE_LOAD_ERROR_CONFLICT 2
#define MB_NATIVE_LOAD_ERROR_EXPORT 3
#define MB_NATIVE_LOAD_ERROR_VERSION 4
#define MB_NATIVE_LOAD_ERROR_ABI 5

int mb_native_load(const wchar_t *path, char **error, int *load_code);
int mb_native_retain_loaded(void);
void mb_native_release(void);
int mb_native_loaded(void);

uint32_t mb_abi_version(void);
const char *mb_native_version(void);
const char *mb_frida_core_version(void);
const char *mb_zlib_version(void);
int mb_zlib_compress(const uint8_t *input, size_t input_size, uint8_t **output, size_t *output_size, char **error);
int mb_zlib_decompress(const uint8_t *input, size_t input_size, size_t expected_size, size_t max_output, uint8_t **output, size_t *output_size, char **error);
void mb_bytes_free(uint8_t *bytes);
mb_device *mb_device_open(char **error);
int mb_device_enumerate(mb_device *device, mb_process **items, size_t *count, char **error);
void mb_processes_free(mb_process *items, size_t count);
mb_session *mb_device_attach(mb_device *device, uint32_t pid, uintptr_t handle, mb_detached_cb callback, char **error);
void mb_device_close(mb_device *device);
void mb_runtime_shutdown(void);
mb_script *mb_session_load_script(mb_session *session, const char *source, uintptr_t handle, mb_message_cb callback, char **error);
int mb_session_detach(mb_session *session, char **error);
int mb_script_post(mb_script *script, const char *json, char **error);
int mb_script_unload(mb_script *script, char **error);
void mb_error_free(char *error);

#endif
