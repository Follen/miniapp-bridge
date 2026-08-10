package sdk

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewDefaultsAndAllocationOnlyContract(t *testing.T) {
	var nativeStarts atomic.Int32
	recordPath := filepath.Join(t.TempDir(), "capture.bin")
	service, err := New(Options{
		RecordPath: recordPath,
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			nativeStarts.Add(1)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if status.State != StateNew || status.DebugPort != DefaultDebugPort || status.CDPPort != DefaultCDPPort {
		t.Fatalf("New status=%+v", status)
	}
	if nativeStarts.Load() != 0 {
		t.Fatal("New invoked the native starter")
	}
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("New touched the recording path: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	debugListener := listenForConstructionTest(t)
	defer debugListener.Close()
	cdpListener := listenForConstructionTest(t)
	defer cdpListener.Close()
	occupied, err := New(Options{
		DebugPort: debugListener.Addr().(*net.TCPAddr).Port,
		CDPPort:   cdpListener.Addr().(*net.TCPAddr).Port,
		Native:    disabledNativeStarter,
	})
	if err != nil {
		t.Fatalf("New attempted to bind listeners: %v", err)
	}
	if err := occupied.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func listenForConstructionTest(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func TestSDKAndCoreDoNotExitOrRegisterSignals(t *testing.T) {
	t.Helper()
	for _, root := range []string{".", "../internal"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return err
			}
			aliases := make(map[string]string)
			for _, imported := range file.Imports {
				name := ""
				if imported.Name != nil {
					name = imported.Name.Name
				}
				importPath, err := strconv.Unquote(imported.Path.Value)
				if err != nil || name == "_" || name == "." {
					continue
				}
				if name == "" {
					name = filepath.Base(importPath)
				}
				aliases[name] = importPath
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				qualifier, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				forbidden := aliases[qualifier.Name] == "os" && selector.Sel.Name == "Exit"
				forbidden = forbidden || aliases[qualifier.Name] == "log" && strings.HasPrefix(selector.Sel.Name, "Fatal")
				forbidden = forbidden || aliases[qualifier.Name] == "os/signal" && strings.HasPrefix(selector.Sel.Name, "Notify")
				if forbidden {
					t.Errorf("production SDK/core calls %s.%s at %s", qualifier.Name, selector.Sel.Name, set.Position(call.Pos()))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}
}
