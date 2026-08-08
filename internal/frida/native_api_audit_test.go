package frida

import (
	"os"
	"strings"
	"testing"
)

func TestAuditFridaCoreVersionIsPinned(t *testing.T) {
	t.Parallel()
	source := readAuditSource(t, "native_windows.go")
	if !strings.Contains(source, "runtime-17.3.2") {
		t.Fatal("native cgo linker path does not pin frida-core 17.3.2")
	}
}

func TestAuditNativeEnumerationUsesReferenceMetadataScope(t *testing.T) {
	t.Parallel()
	source := readAuditSource(t, "shim/miniapp_frida.c")
	if !strings.Contains(source, "frida_process_query_options_set_scope(options, FRIDA_SCOPE_METADATA)") {
		t.Fatal("process enumeration does not use the reference Scope.Metadata")
	}
}

func TestAuditNativeOwnershipDisconnectsCallbacksAndDeinitializesFrida(t *testing.T) {
	t.Parallel()
	source := readAuditSource(t, "shim/miniapp_frida.c")
	required := []string{
		"g_signal_handlers_disconnect_by_data(session->session,session)",
		"frida_unref(session->session)",
		"g_signal_handlers_disconnect_by_data(script->script,script)",
		"frida_unref(script->script)",
		"frida_device_manager_close_sync(device->manager,NULL,NULL)",
		"frida_unref(device->manager)",
		"frida_deinit()",
	}
	for _, token := range required {
		if !strings.Contains(source, token) {
			t.Errorf("native ownership contract missing %q", token)
		}
	}
}

func TestAuditNativeCleanupDoesNotTerminateAttachedProcess(t *testing.T) {
	t.Parallel()
	source := readAuditSource(t, "shim/miniapp_frida.c")
	for _, forbidden := range []string{
		"TerminateProcess", "ExitProcess", "kill(", "frida_device_kill",
		"frida_device_kill_sync", "frida_device_resume",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("native shim unexpectedly contains process-control primitive %q", forbidden)
		}
	}

	assertOrderedTokens(t, source, "mb_script_unload", []string{
		"g_signal_handlers_disconnect_by_data(script->script,script)",
		"frida_script_unload_sync(script->script,NULL,&e)",
		"frida_unref(script->script)",
		"free(script)",
	})
	assertOrderedTokens(t, source, "mb_session_detach", []string{
		"g_signal_handlers_disconnect_by_data(session->session,session)",
		"frida_session_detach_sync(session->session,NULL,&e)",
		"frida_unref(session->session)",
		"free(session)",
	})
	assertOrderedTokens(t, source, "mb_device_close", []string{
		"frida_unref(device->device)",
		"frida_device_manager_close_sync(device->manager,NULL,NULL)",
		"frida_unref(device->manager)",
		"free(device)",
		"mb_frida_release()",
	})
}

func assertOrderedTokens(t *testing.T, source, function string, tokens []string) {
	t.Helper()
	start := strings.Index(source, function)
	if start < 0 {
		t.Fatalf("native shim function %q not found", function)
	}
	remaining := source[start:]
	for _, token := range tokens {
		index := strings.Index(remaining, token)
		if index < 0 {
			t.Fatalf("native shim function %q missing ordered cleanup %q", function, token)
		}
		remaining = remaining[index+len(token):]
	}
}

func readAuditSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
