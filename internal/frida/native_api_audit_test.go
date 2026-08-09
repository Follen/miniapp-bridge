package frida

import (
	"os"
	"sort"
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

func TestAuditNativeABIExportsAreCompleteAndPinned(t *testing.T) {
	t.Parallel()
	expected := []string{
		"mb_abi_version", "mb_native_version", "mb_frida_core_version", "mb_zlib_version",
		"mb_zlib_compress", "mb_zlib_decompress", "mb_bytes_free",
		"mb_device_open", "mb_device_enumerate", "mb_processes_free", "mb_device_attach",
		"mb_device_close", "mb_runtime_shutdown", "mb_session_load_script", "mb_session_detach",
		"mb_script_post", "mb_script_unload", "mb_error_free",
	}
	def := readAuditSource(t, "shim/miniapp_frida.def")
	header := readAuditSource(t, "shim/miniapp_frida.h")
	loader := readAuditSource(t, "loader_windows.inc")
	release := readAuditSource(t, "../../scripts/native-release.ps1")
	for _, notice := range []string{"'LICENSE'", "'ZLIB_LICENSE'", "'THIRD_PARTY_NOTICES.md'"} {
		if !strings.Contains(release, notice) {
			t.Errorf("release archive missing notice %s", notice)
		}
	}

	var actual []string
	for _, line := range strings.Split(def, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mb_") {
			actual = append(actual, line)
		}
	}
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if strings.Join(actual, "\n") != strings.Join(want, "\n") {
		t.Fatalf("shim exports mismatch\ngot:\n%s\nwant:\n%s", strings.Join(actual, "\n"), strings.Join(want, "\n"))
	}
	for _, name := range expected {
		if !strings.Contains(header, name+"(") {
			t.Errorf("shim header missing %s", name)
		}
		if !strings.Contains(release, "'"+name+"'") {
			t.Errorf("release manifest missing %s", name)
		}
		resolver := strings.TrimPrefix(name, "mb_")
		if strings.HasSuffix(name, "_version") || strings.HasPrefix(name, "mb_zlib_") || name == "mb_bytes_free" || strings.HasPrefix(name, "mb_device_") ||
			strings.HasPrefix(name, "mb_session_") || strings.HasPrefix(name, "mb_script_") ||
			name == "mb_processes_free" || name == "mb_runtime_shutdown" || name == "mb_error_free" {
			if !strings.Contains(loader, "RESOLVE("+resolver+",") {
				t.Errorf("loader does not resolve %s", name)
			}
		}
	}
	for path, source := range map[string]string{"shim header": header, "loader header": readAuditSource(t, "loader_windows.h")} {
		for _, token := range []string{"#define MB_ZLIB_VERSION \"1.3.1\"", "mb_zlib_version"} {
			if !strings.Contains(source, token) {
				t.Errorf("%s missing %q", path, token)
			}
		}
	}
}

func TestAuditNativeLoaderDiagnosticsAndReferenceCounting(t *testing.T) {
	t.Parallel()
	loader := readAuditSource(t, "loader_windows.inc")
	for _, token := range []string{
		"LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32",
		"FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS",
		"missing export: mb_%s",
		"a different native runtime is already loaded",
		"native runtime ABI version mismatch",
		"native runtime version mismatch",
		"frida-core version mismatch",
		"zlib version mismatch",
	} {
		if !strings.Contains(loader, token) {
			t.Errorf("native loader diagnostic contract missing %q", token)
		}
	}
	assertOrderedTokens(t, loader, "int mb_native_load", []string{
		"_wcsicmp(g_path, path) == 0", "g_refs++", "g_refs = 1",
	})
	assertOrderedTokens(t, loader, "void mb_native_release", []string{
		"if (g_refs > 0) g_refs--", "if (g_refs == 0 && g_module != NULL)",
		"g_module = NULL", "free(g_path)", "p_runtime_shutdown()", "clear_functions()", "FreeLibrary(module)",
	})
	assertOrderedTokens(t, loader, "int mb_native_loaded", []string{
		"ensure_lock()", "EnterCriticalSection(&g_lock)", "g_module != NULL", "LeaveCriticalSection(&g_lock)",
	})
	assertOrderedTokens(t, loader, "int mb_native_retain_loaded", []string{
		"ensure_lock()", "EnterCriticalSection(&g_lock)", "g_module != NULL", "g_refs++", "LeaveCriticalSection(&g_lock)",
	})
	assertOrderedTokens(t, loader, "static void copy_native_error", []string{
		"_strdup(native_error)", "p_error_free(native_error)",
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
