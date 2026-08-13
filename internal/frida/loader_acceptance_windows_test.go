//go:build windows

package frida

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsLoaderFakeDLLAcceptance(t *testing.T) {
	vsdev := `C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\Tools\VsDevCmd.bat`
	if _, err := os.Stat(vsdev); err != nil {
		t.Skipf("MSVC Build Tools unavailable: %v", err)
	}
	work := t.TempDir()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	include := filepath.Join(repo, "internal", "frida")
	writeLoaderFixture(t, filepath.Join(work, "good.c"), loaderFixtureSpec{
		prefix: `
#include <stdio.h>
#include <windows.h>
static void loader_trace(const char *event) {
  char path[MAX_PATH]; DWORD size = GetEnvironmentVariableA("MB_LOADER_TRACE", path, MAX_PATH);
  if (size == 0 || size >= MAX_PATH) return;
 FILE *file = fopen(path, "ab"); if (file != NULL) { fputs(event, file); fputc('\n', file); fclose(file); }
 }
static void loader_active_delay(void) {
  char name[256]; DWORD size = GetEnvironmentVariableA("MB_LOADER_ACTIVE_EVENT", name, sizeof(name));
  if (size > 0 && size < sizeof(name)) { HANDLE event = OpenEventA(EVENT_MODIFY_STATE, FALSE, name); if (event != NULL) { SetEvent(event); CloseHandle(event); } }
  Sleep(100);
}
#define MB_ZLIB_DELAY() loader_active_delay()
BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID reserved) {
  (void)instance; (void)reserved; if (reason == DLL_PROCESS_DETACH) loader_trace("unload"); return TRUE;
}`,
		shutdownBody: `loader_trace("shutdown");`,
	})
	writeLoaderFixture(t, filepath.Join(work, "bad-zlib.c"), loaderFixtureSpec{zlibVersion: `"1.3.2"`})
	writeLoaderFixture(t, filepath.Join(work, "bad-native.c"), loaderFixtureSpec{nativeVersion: `"17.3.1-abi1"`})
	writeLoaderFixture(t, filepath.Join(work, "bad-frida.c"), loaderFixtureSpec{fridaVersion: `"17.3.1"`})
	writeLoaderFixture(t, filepath.Join(work, "bad-abi.c"), loaderFixtureSpec{abiVersion: "2"})
	writeLoaderFixture(t, filepath.Join(work, "null-version.c"), loaderFixtureSpec{nativeVersion: "NULL"})
	writeLoaderFixture(t, filepath.Join(work, "missing.c"), loaderFixtureSpec{omitScriptPost: true})
	writeLoaderFixture(t, filepath.Join(work, "wrong-arch.c"), loaderFixtureSpec{})
	writeLoaderFixture(t, filepath.Join(work, "dependency.c"), loaderFixtureSpec{
		prefix:     `__declspec(dllimport) int loader_fixture_dependency(void);`,
		abiVersion: `(loader_fixture_dependency(), 1)`,
	})
	if err := os.WriteFile(filepath.Join(work, "fixture-dependency.c"), []byte(loaderDependencyTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	harnessSource := strings.ReplaceAll(loaderHarnessSourceTemplate, "__LOADER_HEADER__", filepath.ToSlash(filepath.Join(include, "loader_windows.h")))
	harnessSource = strings.ReplaceAll(harnessSource, "__LOADER_INC__", filepath.ToSlash(filepath.Join(include, "loader_windows.inc")))
	if err := os.WriteFile(filepath.Join(work, "harness.c"), []byte(harnessSource), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := []string{
		`cl /nologo /LD /MT /W3 /Fe:"good.dll" "good.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"bad-zlib.dll" "bad-zlib.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"bad-native.dll" "bad-native.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"bad-frida.dll" "bad-frida.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"bad-abi.dll" "bad-abi.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"null-version.dll" "null-version.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"missing.dll" "missing.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"fixture-dependency.dll" "fixture-dependency.c"`,
		`cl /nologo /LD /MT /W3 /Fe:"dependency.dll" "dependency.c" "fixture-dependency.lib"`,
		`cl /nologo /MT /W3 /Fe:"loader-harness.exe" "harness.c"`,
	}
	for _, command := range commands {
		runMSVCCommand(t, work, vsdev, command)
	}
	runMSVCCommandArch(t, work, vsdev, "x86", `cl /nologo /LD /MT /W3 /Fe:"wrong-arch.dll" "wrong-arch.c"`)
	hardlinkSource := filepath.Join(work, "hardlink-source.dll")
	hardlinkPath := filepath.Join(work, "hardlink.dll")
	goodBytes, err := os.ReadFile(filepath.Join(work, "good.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hardlinkSource, goodBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(hardlinkSource, hardlinkPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := os.Remove(filepath.Join(work, "fixture-dependency.dll")); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(work, "loader-harness.exe")
	cmd := exec.Command(harness,
		filepath.Join(work, "good.dll"),
		filepath.Join(work, "bad-zlib.dll"),
		filepath.Join(work, "missing.dll"),
		filepath.Join(work, "bad-native.dll"),
		filepath.Join(work, "bad-frida.dll"),
		filepath.Join(work, "bad-abi.dll"),
		filepath.Join(work, "null-version.dll"),
		filepath.Join(work, "wrong-arch.dll"),
		filepath.Join(work, "dependency.dll"),
		hardlinkPath,
		filepath.Join(work, "loader-trace.txt"),
		`Local\miniapp-bridge-loader-active`,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("loader acceptance failed: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "loader acceptance ok" {
		t.Fatalf("loader output=%q", got)
	}
	trace, err := os.ReadFile(filepath.Join(work, "loader-trace.txt"))
	if err != nil {
		t.Fatal(err)
	}
	events := strings.Fields(string(trace))
	if len(events) == 0 || len(events)%2 != 0 {
		t.Fatalf("loader lifecycle trace=%v", events)
	}
	for i := 0; i < len(events); i += 2 {
		if events[i] != "shutdown" || events[i+1] != "unload" {
			t.Fatalf("loader lifecycle trace=%v", events)
		}
	}
}

func runMSVCCommand(t *testing.T, dir, vsdev, command string) {
	runMSVCCommandArch(t, dir, vsdev, "x64", command)
}

func runMSVCCommandArch(t *testing.T, dir, vsdev, arch, command string) {
	t.Helper()
	line := fmt.Sprintf("call \"%s\" -arch=%s -host_arch=x64 >nul && %s\r\n", vsdev, arch, command)
	batch := filepath.Join(dir, "build-native-fixture.cmd")
	if err := os.WriteFile(batch, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd.exe", "/d", "/s", "/c", batch)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("MSVC command failed: %v\ncommand: %s\n%s", err, command, output)
	}
}

type loaderFixtureSpec struct {
	prefix         string
	abiVersion     string
	nativeVersion  string
	fridaVersion   string
	zlibVersion    string
	omitScriptPost bool
	shutdownBody   string
}

func writeLoaderFixture(t *testing.T, path string, spec loaderFixtureSpec) {
	t.Helper()
	if spec.abiVersion == "" {
		spec.abiVersion = "1"
	}
	if spec.nativeVersion == "" {
		spec.nativeVersion = `"17.3.2-abi1"`
	}
	if spec.fridaVersion == "" {
		spec.fridaVersion = `"17.3.2"`
	}
	if spec.zlibVersion == "" {
		spec.zlibVersion = `"1.3.1"`
	}
	if spec.shutdownBody == "" {
		spec.shutdownBody = `(void)0;`
	}
	scriptPost := ""
	if !spec.omitScriptPost {
		scriptPost = `__declspec(dllexport) int mb_script_post(mb_script *s,const char *j,char **e){(void)s;(void)j;(void)e;return 1;}`
	}
	source := fmt.Sprintf(loaderFixtureTemplateExtended, spec.prefix, spec.abiVersion, spec.nativeVersion, spec.fridaVersion, spec.zlibVersion, spec.shutdownBody, scriptPost)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

// loaderFixtureTemplate is retained for native_zlib_windows_test.go, which
// uses the original two-argument fixture format.
const loaderFixtureTemplate = `
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
typedef struct mb_device mb_device; typedef struct mb_session mb_session; typedef struct mb_script mb_script;
typedef struct { uint32_t pid; uint32_t ppid; char *name; char *path; } mb_process;
typedef void (*mb_message_cb)(uintptr_t,char*,uint8_t*,size_t); typedef void (*mb_detached_cb)(uintptr_t,int);
#ifndef MB_ZLIB_DELAY
#define MB_ZLIB_DELAY() ((void)0)
#endif
__declspec(dllexport) uint32_t mb_abi_version(void){return 1;}
__declspec(dllexport) const char *mb_native_version(void){return "17.3.2-abi1";}
__declspec(dllexport) const char *mb_frida_core_version(void){return "17.3.2";}
__declspec(dllexport) const char *mb_zlib_version(void){return "%s";}
__declspec(dllexport) int mb_zlib_compress(const uint8_t*i,size_t n,uint8_t**o,size_t*s,char**e){(void)e;MB_ZLIB_DELAY();*o=(uint8_t*)malloc(n?n:1);if(!*o)return 0;if(n)memcpy(*o,i,n);*s=n;return 1;}
__declspec(dllexport) int mb_zlib_decompress(const uint8_t*i,size_t n,size_t x,size_t m,uint8_t**o,size_t*s,char**e){(void)x;(void)m;return mb_zlib_compress(i,n,o,s,e);}
__declspec(dllexport) void mb_bytes_free(uint8_t*b){free(b);}
__declspec(dllexport) mb_device *mb_device_open(char **e){(void)e;return (mb_device*)1;}
__declspec(dllexport) int mb_device_enumerate(mb_device*d,mb_process**i,size_t*c,char**e){(void)d;(void)i;(void)c;(void)e;return 1;}
__declspec(dllexport) void mb_processes_free(mb_process*i,size_t c){(void)i;(void)c;}
__declspec(dllexport) mb_session *mb_device_attach(mb_device*d,uint32_t p,uintptr_t h,mb_detached_cb cb,char**e){(void)d;(void)p;(void)h;(void)cb;(void)e;return (mb_session*)1;}
__declspec(dllexport) void mb_device_close(mb_device*d){(void)d;}
__declspec(dllexport) void mb_runtime_shutdown(void){}
__declspec(dllexport) mb_script *mb_session_load_script(mb_session*s,const char*src,uintptr_t h,mb_message_cb cb,char**e){(void)s;(void)src;(void)h;(void)cb;(void)e;return (mb_script*)1;}
__declspec(dllexport) int mb_session_detach(mb_session*s,char**e){(void)s;(void)e;return 1;}
%s
__declspec(dllexport) int mb_script_unload(mb_script*s,char**e){(void)s;(void)e;return 1;}
__declspec(dllexport) void mb_error_free(char*e){(void)e;}
`

const loaderFixtureTemplateExtended = `
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
typedef struct mb_device mb_device; typedef struct mb_session mb_session; typedef struct mb_script mb_script;
typedef struct { uint32_t pid; uint32_t ppid; char *name; char *path; } mb_process;
typedef void (*mb_message_cb)(uintptr_t,char*,uint8_t*,size_t); typedef void (*mb_detached_cb)(uintptr_t,int);
%s
#ifndef MB_ZLIB_DELAY
#define MB_ZLIB_DELAY() ((void)0)
#endif
__declspec(dllexport) uint32_t mb_abi_version(void){return %s;}
__declspec(dllexport) const char *mb_native_version(void){return %s;}
__declspec(dllexport) const char *mb_frida_core_version(void){return %s;}
__declspec(dllexport) const char *mb_zlib_version(void){return %s;}
__declspec(dllexport) int mb_zlib_compress(const uint8_t*i,size_t n,uint8_t**o,size_t*s,char**e){(void)e;MB_ZLIB_DELAY();*o=(uint8_t*)malloc(n?n:1);if(!*o)return 0;if(n)memcpy(*o,i,n);*s=n;return 1;}
__declspec(dllexport) int mb_zlib_decompress(const uint8_t*i,size_t n,size_t x,size_t m,uint8_t**o,size_t*s,char**e){(void)x;(void)m;return mb_zlib_compress(i,n,o,s,e);}
__declspec(dllexport) void mb_bytes_free(uint8_t*b){free(b);}
__declspec(dllexport) mb_device *mb_device_open(char **e){(void)e;return (mb_device*)1;}
__declspec(dllexport) int mb_device_enumerate(mb_device*d,mb_process**i,size_t*c,char**e){(void)d;(void)i;(void)c;(void)e;return 1;}
__declspec(dllexport) void mb_processes_free(mb_process*i,size_t c){(void)i;(void)c;}
__declspec(dllexport) mb_session *mb_device_attach(mb_device*d,uint32_t p,uintptr_t h,mb_detached_cb cb,char**e){(void)d;(void)p;(void)h;(void)cb;(void)e;return (mb_session*)1;}
__declspec(dllexport) void mb_device_close(mb_device*d){(void)d;}
__declspec(dllexport) void mb_runtime_shutdown(void){%s}
__declspec(dllexport) mb_script *mb_session_load_script(mb_session*s,const char*src,uintptr_t h,mb_message_cb cb,char**e){(void)s;(void)src;(void)h;(void)cb;(void)e;return (mb_script*)1;}
__declspec(dllexport) int mb_session_detach(mb_session*s,char**e){(void)s;(void)e;return 1;}
%s
__declspec(dllexport) int mb_script_unload(mb_script*s,char**e){(void)s;(void)e;return 1;}
__declspec(dllexport) void mb_error_free(char*e){(void)e;}
`

const loaderDependencyTemplate = `
__declspec(dllexport) int loader_fixture_dependency(void) { return 1; }
`

const loaderHarnessSourceTemplate = `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>
#include "__LOADER_HEADER__"
#include "__LOADER_INC__"
static int expect_failure(const wchar_t *path, const char *needle, int want_code) {
  char *error = NULL;
  int code = MB_NATIVE_LOAD_OK;
  if (mb_native_load(path, &error, &code)) { mb_native_release(); fwprintf(stderr,L"unexpected load success: %ls\n",path); return 0; }
  if (error == NULL || strstr(error, needle) == NULL) { fprintf(stderr,"error mismatch: got=%s want=%s\n",error?error:"<null>",needle); mb_error_free(error); return 0; }
  if (code != want_code) { fprintf(stderr,"load code mismatch: got=%d want=%d\n",code,want_code); mb_error_free(error); return 0; }
  mb_error_free(error);
  if (mb_native_loaded() || mb_native_retain_loaded() || mb_abi_version() != 0 || strcmp(mb_native_version(),"") != 0 || strcmp(mb_frida_core_version(),"") != 0 || strcmp(mb_zlib_version(),"") != 0) {
    fprintf(stderr,"loader retained state after failed load\n"); return 0;
  }
  return 1;
}
static DWORD WINAPI load_release_worker(LPVOID parameter) {
  const wchar_t *path = (const wchar_t *)parameter;
  for (int i = 0; i < 100; i++) {
    char *error = NULL; int code = MB_NATIVE_LOAD_OK;
    if (!mb_native_load(path, &error, &code)) return 1;
    mb_native_release();
  }
  return 0;
}
static DWORD WINAPI active_call_worker(LPVOID parameter) {
  (void)parameter;
  uint8_t input[1] = {7}; uint8_t *output = NULL; size_t output_size = 0; char *error = NULL;
  int ok = mb_zlib_compress(input, 1, &output, &output_size, &error);
  if (error != NULL) mb_error_free(error);
  if (output != NULL) mb_bytes_free(output);
  return ok && output_size == 1 ? 0 : 1;
}
int wmain(int argc, wchar_t **argv) {
  char *error = NULL;
  int code = MB_NATIVE_LOAD_OK;
  uint8_t input[3] = {1,2,3}; uint8_t *output = NULL; size_t output_size = 0;
  HANDLE workers[8];
  if (argc != 13) return 2;
  if (_wputenv_s(L"MB_LOADER_TRACE", argv[11]) != 0) return 26;
  if (_wputenv_s(L"MB_LOADER_ACTIVE_EVENT", argv[12]) != 0) return 27;
  if (!mb_native_load(argv[1], &error, &code) || code != MB_NATIVE_LOAD_OK || !mb_native_load(argv[1], &error, &code) || code != MB_NATIVE_LOAD_OK) return 3;
  if (!mb_native_loaded()) return 4;
  if (!mb_native_retain_loaded()) return 13;
  if (mb_abi_version() != 1 || strcmp(mb_native_version(),"17.3.2-abi1") != 0 || strcmp(mb_frida_core_version(),"17.3.2") != 0 || strcmp(mb_zlib_version(),"1.3.1") != 0) return 16;
  if (!mb_zlib_compress(input,3,&output,&output_size,&error) || output_size != 3 || memcmp(input,output,3) != 0) return 14;
  mb_bytes_free(output); output = NULL; output_size = 0;
  if (!mb_zlib_decompress(input,3,3,16,&output,&output_size,&error) || output_size != 3 || memcmp(input,output,3) != 0) return 15;
  mb_bytes_free(output); mb_native_release();
  mb_native_release(); if (!mb_native_loaded()) return 5;
  if (mb_native_load(argv[2], &error, &code)) return 6;
  if (error == NULL || strstr(error,"different native runtime") == NULL || code != MB_NATIVE_LOAD_ERROR_CONFLICT) return 7;
  mb_error_free(error); error = NULL;
  mb_native_release(); if (mb_native_loaded()) return 8;
  HANDLE active_event = CreateEventW(NULL, TRUE, FALSE, argv[12]); if (active_event == NULL) return 28;
  if (!mb_native_load(argv[1], &error, &code)) return 29;
  HANDLE active_thread = CreateThread(NULL, 0, active_call_worker, NULL, 0, NULL); if (active_thread == NULL) return 30;
  if (WaitForSingleObject(active_event, 2000) != WAIT_OBJECT_0) return 31;
  mb_native_release(); if (!mb_native_loaded()) return 32;
  if (WaitForSingleObject(active_thread, INFINITE) != WAIT_OBJECT_0) return 33;
  DWORD active_result = 1; GetExitCodeThread(active_thread, &active_result); CloseHandle(active_thread); CloseHandle(active_event);
  if (active_result != 0 || mb_native_loaded()) return 34;
  if (!mb_native_load(argv[1], &error, &code)) return 35;
  mb_device *device = mb_device_open(&error); if (device == NULL) return 36;
  mb_session *session = mb_device_attach(device, 1, 0, NULL, &error); if (session == NULL) return 37;
  mb_script *script = mb_session_load_script(session, "send(1)", 0, NULL, &error); if (script == NULL) return 38;
  mb_native_release(); if (!mb_native_loaded()) return 39;
  HANDLE blocked = CreateFileW(argv[1], GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, 0, NULL);
  if (blocked != INVALID_HANDLE_VALUE) { CloseHandle(blocked); return 40; }
  if (GetLastError() != ERROR_SHARING_VIOLATION) return 41;
  if (!mb_script_unload(script, &error) || !mb_native_loaded()) return 42;
  if (!mb_session_detach(session, &error) || !mb_native_loaded()) return 43;
  mb_device_close(device); if (mb_native_loaded()) return 44;
  if (!expect_failure(argv[2],"zlib version mismatch",MB_NATIVE_LOAD_ERROR_VERSION)) return 9;
  if (!expect_failure(argv[3],"missing export: mb_script_post",MB_NATIVE_LOAD_ERROR_EXPORT)) return 10;
  if (!expect_failure(argv[4],"native runtime version mismatch",MB_NATIVE_LOAD_ERROR_VERSION)) return 17;
  if (!expect_failure(argv[5],"frida-core version mismatch",MB_NATIVE_LOAD_ERROR_VERSION)) return 18;
  if (!expect_failure(argv[6],"native runtime ABI version mismatch",MB_NATIVE_LOAD_ERROR_ABI)) return 19;
  if (!expect_failure(argv[7],"native runtime version exports returned NULL",MB_NATIVE_LOAD_ERROR_VERSION)) return 20;
  if (!expect_failure(argv[8],"win32=193",MB_NATIVE_LOAD_ERROR_LOAD)) return 21;
  if (!expect_failure(argv[9],"win32=126",MB_NATIVE_LOAD_ERROR_LOAD)) return 22;
  if (!expect_failure(L"","native runtime path is empty",MB_NATIVE_LOAD_ERROR_LOAD)) return 11;
  if (!expect_failure(L"Z:\\miniapp-bridge-does-not-exist\\missing.dll","LoadLibraryExW failed (win32=",MB_NATIVE_LOAD_ERROR_LOAD)) return 12;
  if (!expect_failure(argv[10],"exactly one hard link",MB_NATIVE_LOAD_ERROR_LOAD)) return 45;
  for (int i = 0; i < 8; i++) workers[i] = CreateThread(NULL,0,load_release_worker,argv[1],0,NULL);
  if (WaitForMultipleObjects(8,workers,TRUE,INFINITE) != WAIT_OBJECT_0) return 23;
  for (int i = 0; i < 8; i++) { DWORD result = 1; GetExitCodeThread(workers[i],&result); CloseHandle(workers[i]); if (result != 0) return 24; }
  if (mb_native_loaded()) return 25;
  puts("loader acceptance ok"); return 0;
}
`
