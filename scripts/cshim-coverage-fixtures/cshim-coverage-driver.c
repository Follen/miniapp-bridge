#include "cshim-coverage-stubs.h"
#include "../../internal/frida/shim/miniapp_frida.h"

#include <stdio.h>

typedef struct {
  GMutex mutex;
  GCond drained;
  gboolean closing;
  guint in_flight;
  uintptr_t handle;
} mb_callback_owner_view;

struct mb_session {
  FridaSession *session;
  mb_callback_owner_view callback;
  mb_detached_cb detached;
};
struct mb_script {
  FridaScript *script;
  mb_callback_owner_view callback;
  mb_message_cb message;
};

static int failures = 0;
static struct mb_script *active_script;
static struct mb_session *active_session;

static void check(int condition, const char *label) {
  if (!condition) {
    fprintf(stderr, "cshim coverage assertion failed: %s\n", label);
    failures++;
  }
}

static void clear_error(char **error) {
  if (*error != NULL) {
    mb_error_free(*error);
    *error = NULL;
  }
}

static void message_callback(uintptr_t handle, char *message, uint8_t *data, size_t size) {
  check(handle == 0x1234u, "message handle");
  check(message != NULL, "message text");
  check(data == NULL || size == 3, "message payload");
  if (active_script != NULL) active_script->callback.closing = TRUE;
}

static void detached_callback(uintptr_t handle, int reason) {
  check(handle == 0x4321u, "detached handle");
  check(reason == 7, "detached reason");
  if (active_session != NULL) active_session->callback.closing = TRUE;
}

static void cover_zlib(void) {
  const uint8_t input[] = {'a', 'b', 'c'};
  uint8_t *output = NULL;
  size_t output_size = 0;
  char *error = NULL;

  check(mb_zlib_compress(input, sizeof(input), NULL, &output_size, &error) == 0, "compress null output");
  clear_error(&error);
  check(mb_zlib_compress(input, sizeof(input), &output, NULL, &error) == 0, "compress null size");
  clear_error(&error);
  check(mb_zlib_compress(NULL, 1, &output, &output_size, &error) == 0, "compress invalid input");
  clear_error(&error);
  check(mb_zlib_compress(input, sizeof(input), &output, &output_size, &error) == 1, "compress success");
  check(output_size == sizeof(input), "compress size");
  mb_bytes_free(output);
  output = NULL;

  mb_cov_decompress_mode = 9;
  check(mb_zlib_compress(NULL, 0, &output, &output_size, &error) == 1, "compress zero capacity");
  mb_bytes_free(output);
  output = NULL;
  mb_cov_decompress_mode = 0;

  mb_cov_fail_compress = 1;
  check(mb_zlib_compress(input, sizeof(input), &output, &output_size, &error) == 0, "compress failure");
  clear_error(&error);
  mb_cov_fail_compress = 0;
  mb_cov_fail_malloc_once = 1;
  check(mb_zlib_compress(input, sizeof(input), &output, &output_size, &error) == 0, "compress allocation failure");
  clear_error(&error);

  check(mb_zlib_decompress(input, sizeof(input), 0, 0, &output, &output_size, &error) == 0, "decompress max zero");
  clear_error(&error);
  check(mb_zlib_decompress(NULL, 1, 0, 16, &output, &output_size, &error) == 0, "decompress invalid input");
  clear_error(&error);
  check(mb_zlib_decompress(input, sizeof(input), 5, 4, &output, &output_size, &error) == 0, "decompress expected over max");
  clear_error(&error);
  check(mb_zlib_decompress(input, sizeof(input), 268435457u, 400000000u, &output, &output_size, &error) == 0,
        "decompress expected over hard max");
  clear_error(&error);
  check(mb_zlib_decompress(input, sizeof(input), 3, 16, &output, &output_size, &error) == 1, "decompress expected success");
  mb_bytes_free(output);
  output = NULL;

  mb_cov_decompress_mode = 3;
  check(mb_zlib_decompress(input, 1, 3, 16, &output, &output_size, &error) == 0, "decompress size mismatch");
  clear_error(&error);
  mb_cov_decompress_mode = 0;
  check(mb_zlib_decompress(input, 1, 0, 256, &output, &output_size, &error) == 1, "decompress small inferred capacity");
  mb_bytes_free(output);
  output = NULL;
  mb_cov_decompress_mode = 1;
  mb_cov_decompress_calls = 0;
  check(mb_zlib_decompress(input, 1, 0, 512, &output, &output_size, &error) == 1, "decompress buffer retry");
  mb_bytes_free(output);
  output = NULL;
  mb_cov_decompress_mode = 0;
  check(mb_zlib_decompress(input, 100, 0, 300, &output, &output_size, &error) == 1, "decompress bounded inferred capacity");
  mb_bytes_free(output);
  output = NULL;
  mb_cov_decompress_mode = 2;
  check(mb_zlib_decompress(input, sizeof(input), 0, 16, &output, &output_size, &error) == 0, "decompress hard failure");
  clear_error(&error);
  mb_cov_decompress_mode = 0;
  mb_cov_fail_malloc_once = 1;
  check(mb_zlib_decompress(input, sizeof(input), 0, 16, &output, &output_size, &error) == 0, "decompress allocation failure");
  clear_error(&error);
  check(mb_abi_version() == 1u, "abi version");
  check(mb_native_version()[0] != '\0', "native version");
  check(mb_frida_core_version()[0] != '\0', "core version");
  check(mb_zlib_version()[0] != '\0', "zlib version");
}

static mb_device *open_device(void) {
  char *error = NULL;
  mb_device *device = mb_device_open(&error);
  check(device != NULL, "device open");
  clear_error(&error);
  return device;
}

static void cover_devices(void) {
  char *error = NULL;
  mb_process *items = NULL;
  size_t count = 0;

  mb_runtime_shutdown();
  mb_cov_fail_init = 1;
  check(mb_device_open(&error) == NULL, "runtime init failure");
  clear_error(&error);
  mb_cov_fail_init = 0;

  mb_device *device = open_device();
  mb_device *second = open_device();

  mb_cov_fail_cancellable = 1;
  check(mb_device_open(&error) == NULL, "deadline cancellable allocation failure");
  clear_error(&error);
  check(mb_cov_watchdogs_active == 0, "cancellable failure leaves no watchdog");
  mb_cov_fail_deadline_event = 1;
  check(mb_device_open(&error) == NULL, "deadline event creation failure");
  clear_error(&error);
  check(mb_cov_watchdogs_active == 0, "event failure leaves no watchdog");
  mb_cov_fail_deadline_thread = 1;
  check(mb_device_open(&error) == NULL, "deadline thread creation failure");
  clear_error(&error);
  check(mb_cov_watchdogs_active == 0, "thread failure leaves no watchdog");
  mb_cov_wait_for_cancel = 1;
  mb_cov_force_deadline_timeout = 1;
  mb_device *timed = mb_device_open(&error);
  check(timed != NULL, "deadline cancellation returns from sync call");
  clear_error(&error);
  check(mb_cov_cancel_count > 0, "deadline cancelled native call");
  check(mb_cov_watchdogs_active == 0, "timeout leaves no watchdog");
  mb_device_close(timed);
  check(mb_cov_watchdogs_active == 0, "close joins watchdog");

  mb_cov_fail_device = 1;
  check(mb_device_open(&error) == NULL, "device lookup failure");
  clear_error(&error);
  mb_cov_fail_device = 0;

  mb_cov_fail_calloc_once = 1;
  check(mb_device_open(&error) == NULL, "device owner allocation failure");
  clear_error(&error);

  mb_cov_fail_enumerate = 1;
  check(mb_device_enumerate(device, &items, &count, &error) == 0, "enumerate failure");
  clear_error(&error);
  mb_cov_fail_enumerate = 0;
  check(mb_device_enumerate(device, &items, &count, &error) == 1, "enumerate success");
  check(count >= 6, "enumerate count");
  mb_processes_free(items, count);
  mb_processes_free(NULL, 0);
  mb_cov_fail_calloc_once = 1;
  check(mb_device_enumerate(device, &items, &count, &error) == 0, "enumerate allocation failure");
  clear_error(&error);

  mb_cov_fail_attach = 1;
  check(mb_device_attach(device, 101, 0x4321u, detached_callback, &error) == NULL, "attach failure");
  clear_error(&error);
  mb_cov_fail_attach = 0;
  mb_cov_fail_calloc_once = 1;
  check(mb_device_attach(device, 101, 0x4321u, detached_callback, &error) == NULL, "attach owner allocation failure");
  clear_error(&error);
  mb_session *session = mb_device_attach(device, 101, 0x4321u, detached_callback, &error);
  check(session != NULL, "attach success");
  active_session = session;
  if (mb_cov_detached_callback != NULL) {
    mb_cov_detached_callback(((struct mb_session *)session)->session, 7, NULL, mb_cov_detached_user_data);
  }
  ((struct mb_session *)session)->callback.closing = TRUE;
  if (mb_cov_detached_callback != NULL) {
    mb_cov_detached_callback(((struct mb_session *)session)->session, 7, NULL, mb_cov_detached_user_data);
  }
  active_session = NULL;
  check(mb_session_detach(NULL, &error) == 1, "detach null");
  check(mb_session_detach(session, &error) == 1, "detach success");
  clear_error(&error);

  session = mb_device_attach(device, 102, 0x4321u, detached_callback, &error);
  mb_cov_fail_detach = 1;
  check(mb_session_detach(session, &error) == 0, "detach error");
  clear_error(&error);
  mb_cov_fail_detach = 0;

  session = mb_device_attach(device, 103, 0x4321u, NULL, &error);
  check(session != NULL, "script session");
  struct mb_session *session_view = (struct mb_session *)session;

  mb_cov_fail_create_script = 1;
  check(mb_session_load_script(session, "send(1)", 0x1234u, message_callback, &error) == NULL, "script creation failure");
  clear_error(&error);
  mb_cov_fail_create_script = 0;
  mb_cov_fail_calloc_once = 1;
  check(mb_session_load_script(session, "send(1)", 0x1234u, message_callback, &error) == NULL, "script owner allocation failure");
  clear_error(&error);
  mb_cov_fail_load_script = 1;
  check(mb_session_load_script(session, "send(1)", 0x1234u, message_callback, &error) == NULL, "script load failure");
  clear_error(&error);
  mb_cov_fail_load_script = 0;

  mb_script *script_owner = mb_session_load_script(session, "send(1)", 0x1234u, message_callback, &error);
  check(script_owner != NULL, "script load success");
  active_script = (struct mb_script *)script_owner;
  const uint8_t bytes[] = {'x', 'y', 'z'};
  GBytes message = {bytes, sizeof(bytes)};
  if (mb_cov_message_callback != NULL) {
    mb_cov_message_callback(NULL, "message", &message, mb_cov_message_user_data);
    ((struct mb_script *)script_owner)->callback.closing = FALSE;
    mb_cov_message_callback(NULL, "message", NULL, mb_cov_message_user_data);
  }
  ((struct mb_script *)script_owner)->callback.closing = TRUE;
  if (mb_cov_message_callback != NULL) {
    mb_cov_message_callback(NULL, "closed", &message, mb_cov_message_user_data);
  }
  active_script = NULL;
  check(mb_script_post(NULL, "{}", &error) == 0, "script post null");
  check(mb_script_post(script_owner, "{}", &error) == 1, "script post success");
  mb_cov_drain_in_flight = &((struct mb_script *)script_owner)->callback.in_flight;
  check(mb_script_unload(script_owner, &error) == 1, "script unload success");
  mb_cov_drain_in_flight = NULL;
  clear_error(&error);

  session_view->callback.closing = FALSE;
  script_owner = mb_session_load_script(session, "send(1)", 0x1234u, NULL, &error);
  check(script_owner != NULL, "script null callback load");
  check(mb_script_unload(script_owner, &error) == 1, "script null callback unload");
  clear_error(&error);
  script_owner = mb_session_load_script(session, "send(1)", 0x1234u, message_callback, &error);
  mb_cov_fail_unload = 1;
  check(mb_script_unload(script_owner, &error) == 0, "script unload error");
  clear_error(&error);
  mb_cov_fail_unload = 0;
  check(mb_session_detach(session, &error) == 1, "script session detach");

  mb_device_close(NULL);
  mb_device_close(second);
  mb_device_close(device);
  mb_runtime_shutdown();
}

int main(void) {
  cover_zlib();
  cover_devices();
  if (failures != 0) {
    fprintf(stderr, "cshim coverage driver failures=%d\n", failures);
    return 1;
  }
  puts("cshim coverage driver=passed");
  return 0;
}
