#ifndef MINIAPP_FRIDA_H
#define MINIAPP_FRIDA_H

#include <stddef.h>
#include <stdint.h>

#ifdef _WIN32
#define MB_API __declspec(dllexport)
#else
#define MB_API
#endif

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
#define MB_NATIVE_VERSION "17.3.2-abi1"
#define MB_FRIDA_CORE_VERSION "17.3.2"
#define MB_ZLIB_VERSION "1.3.1"

MB_API uint32_t mb_abi_version(void);
MB_API const char *mb_native_version(void);
MB_API const char *mb_frida_core_version(void);
MB_API const char *mb_zlib_version(void);
MB_API int mb_zlib_compress(const uint8_t *input, size_t input_size, uint8_t **output, size_t *output_size, char **error);
MB_API int mb_zlib_decompress(const uint8_t *input, size_t input_size, size_t expected_size, size_t max_output, uint8_t **output, size_t *output_size, char **error);
MB_API void mb_bytes_free(uint8_t *bytes);

MB_API mb_device *mb_device_open(char **error);
MB_API int mb_device_enumerate(mb_device *device, mb_process **items, size_t *count, char **error);
MB_API void mb_processes_free(mb_process *items, size_t count);
MB_API mb_session *mb_device_attach(mb_device *device, uint32_t pid, uintptr_t handle, mb_detached_cb callback, char **error);
MB_API void mb_device_close(mb_device *device);
MB_API void mb_runtime_shutdown(void);
MB_API mb_script *mb_session_load_script(mb_session *session, const char *source, uintptr_t handle, mb_message_cb callback, char **error);
MB_API int mb_session_detach(mb_session *session, char **error);
MB_API int mb_script_post(mb_script *script, const char *json, char **error);
MB_API int mb_script_unload(mb_script *script, char **error);
MB_API void mb_error_free(char *error);

#endif
