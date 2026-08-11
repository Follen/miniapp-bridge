package main

import (
	"context"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExternalModuleImportsOnlySDK(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Dir(root)
	dir := t.TempDir()
	module := "module example.com/external\n\ngo 1.23\n\nrequire github.com/Follen/miniapp-bridge v0.0.0\n\nreplace github.com/Follen/miniapp-bridge => " + root + "\n"
	program := `package external

import (
  "context"
  "errors"
  "net"
  "testing"
  "github.com/Follen/miniapp-bridge/sdk"
)

type nativeSession struct{}
func (nativeSession) Close(context.Context) error { return nil }

func freePort(t *testing.T) int {
  t.Helper()
  listener, err := net.Listen("tcp", "127.0.0.1:0")
  if err != nil { t.Fatal(err) }
  defer listener.Close()
  return listener.Addr().(*net.TCPAddr).Port
}

func TestSDK(t *testing.T) {
  debugPort, cdpPort := freePort(t), freePort(t)
  for cdpPort == debugPort { cdpPort = freePort(t) }
  s, err := sdk.New(sdk.Options{
    DebugPort: debugPort, CDPPort: cdpPort, AddressConfigDir: "addresses", DebugMain: true,
    Native: func(context.Context, func(sdk.LogEvent)) (sdk.NativeSession, error) { return nativeSession{}, nil },
  })
  if err != nil { t.Fatal(err) }
  if s.Status().State != sdk.StateNew { t.Fatal(s.Status().State) }
  route := sdk.Route{JSContextID: "context-1"}
  if _, err = s.Send(context.Background(), sdk.Request{Method: "Runtime.enable", Route: route}); !errors.Is(err, sdk.ErrNotRunning) { t.Fatal(err) }
  if _, err = s.SendRawRoute(context.Background(), []byte(` + "`" + `{"method":"Runtime.enable"}` + "`" + `), route); !errors.Is(err, sdk.ErrNotRunning) { t.Fatal(err) }
  sub := s.SubscribeCDP(sdk.SubscriptionOptions{Buffer: 1})
  if err = sub.Close(); err != nil { t.Fatal(err) }
  if err = s.Start(context.Background()); err != nil { t.Fatal(err) }
  if err = s.Close(context.Background()); err != nil { t.Fatal(err) }
  _ = sdk.NativeVersion
}
`
	library := `package external

import "github.com/Follen/miniapp-bridge/sdk"

func NativeVersion() string { return sdk.NativeVersion }
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sdk_test.go"), []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sdk.go"), []byte(library), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	environment := append(os.Environ(), "GOWORK=off", "CGO_ENABLED=1", "CGO_CFLAGS=", "CGO_LDFLAGS=")
	tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = environment
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("external module tidy (%s) failed: %v\n%s", runtime.GOOS, err, strings.TrimSpace(string(out)))
	}
	list := exec.CommandContext(ctx, "go", "list", "-deps", "-f", `{{if .CgoFiles}}{{.ImportPath}} {{join .CgoFiles ","}}{{end}}`, "github.com/Follen/miniapp-bridge/sdk")
	list.Dir, list.Env = dir, environment
	if out, err := list.CombinedOutput(); err != nil {
		t.Fatalf("external module list (%s) failed: %v\n%s", runtime.GOOS, err, strings.TrimSpace(string(out)))
	} else if strings.Contains(string(out), "github.com/Follen/miniapp-bridge/internal/wmpf ") {
		t.Fatalf("external SDK unexpectedly requires WMPF cgo/zlib: %s", strings.TrimSpace(string(out)))
	}
	taggedList := exec.CommandContext(ctx, "go", "list", "-tags", "frida", "-deps", "-f", `{{if .CgoFiles}}{{.ImportPath}} {{join .CgoFiles ","}}{{end}}`, "github.com/Follen/miniapp-bridge/sdk")
	taggedList.Dir, taggedList.Env = dir, environment
	if out, err := taggedList.CombinedOutput(); err != nil {
		t.Fatalf("external tagged module list (%s) failed: %v\n%s", runtime.GOOS, err, strings.TrimSpace(string(out)))
	} else if strings.Contains(string(out), "github.com/Follen/miniapp-bridge/internal/wmpf ") {
		t.Fatalf("external tagged SDK unexpectedly requires WMPF cgo/zlib: %s", strings.TrimSpace(string(out)))
	}
	for _, args := range [][]string{{"build", "./..."}, {"test", "./..."}} {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir, cmd.Env = dir, environment
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("external module go %s (%s) failed: %v\n%s", args[0], runtime.GOOS, err, strings.TrimSpace(string(out)))
		}
	}
	if runtime.GOOS == "windows" {
		taggedBinary := filepath.Join(dir, "external-frida.test.exe")
		compile := exec.CommandContext(ctx, "go", "test", "-c", "-tags", "frida", "-o", taggedBinary, ".")
		compile.Dir, compile.Env = dir, environment
		if out, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("external tagged module build failed: %v\n%s", err, strings.TrimSpace(string(out)))
		}
		image, err := pe.Open(taggedBinary)
		if err != nil {
			t.Fatal(err)
		}
		libraries, err := image.ImportedLibraries()
		_ = image.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, library := range libraries {
			if strings.EqualFold(library, "zlib1.dll") || strings.EqualFold(library, "miniapp-frida.dll") {
				t.Fatalf("external tagged binary has forbidden static import %q: %v", library, libraries)
			}
		}
	}
}
