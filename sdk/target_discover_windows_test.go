//go:build windows

package sdk

import (
	"context"
	"errors"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/process"
)

func TestDiscoverMapsDetailedWindowsTarget(t *testing.T) {
	original := discoverWindowsProcesses
	t.Cleanup(func() { discoverWindowsProcesses = original })
	discoverWindowsProcesses = func(context.Context) ([]process.Process, error) {
		return []process.Process{{PID: 101, ParentPID: 7, Name: "WeChatAppEx.exe", Path: `C:\Tencent\WMPF\25297\WeChatAppEx.exe`, Version: 25297}}, nil
	}
	s := &Service{}
	got, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{{PID: 101, ParentPID: 7, Name: "WeChatAppEx.exe", Path: `C:\Tencent\WMPF\25297\WeChatAppEx.exe`, Version: 25297}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("targets=%+v want=%+v", got, want)
	}
}

func TestDiscoverWrapsWindowsProcessErrors(t *testing.T) {
	original := discoverWindowsProcesses
	t.Cleanup(func() { discoverWindowsProcesses = original })
	sentinel := errors.New("process query failed")
	discoverWindowsProcesses = func(context.Context) ([]process.Process, error) { return nil, sentinel }
	_, err := (&Service{}).Discover(nil)
	var structured *Error
	if !errors.Is(err, sentinel) || !errors.As(err, &structured) || structured.Op != "discover" || structured.Component != "process" {
		t.Fatalf("error=%v", err)
	}
}
