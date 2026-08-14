//go:build windows

package process

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFindDetailedUsesCIMMetadataAndFiltersNames(t *testing.T) {
	original := queryWindowsProcessesOutput
	t.Cleanup(func() { queryWindowsProcessesOutput = original })
	queryWindowsProcessesOutput = func(context.Context) ([]byte, error) {
		return []byte(`[{
          "ProcessId": 101,
          "Name": "WeChatAppEx.exe",
          "ParentProcessId": 7,
          "ExecutablePath": "C:\\Tencent\\WMPF\\25297\\WeChatAppEx.exe",
          "CommandLine": "--wmpf-appid=app"
        }, {
          "ProcessId": 102,
          "Name": "other.exe",
          "ParentProcessId": 8,
          "ExecutablePath": "C:\\other\\other.exe"
        }]`), nil
	}
	got, err := (TasklistFinder{Names: []string{"wechatappex.exe"}}).FindDetailed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{{PID: 101, ParentPID: 7, Name: "WeChatAppEx.exe", Path: `C:\Tencent\WMPF\25297\WeChatAppEx.exe`, Version: 25297}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detailed=%+v want=%+v", got, want)
	}
}

func TestFindDetailedSupportsSingletonAndEmptyCIMResults(t *testing.T) {
	original := queryWindowsProcessesOutput
	t.Cleanup(func() { queryWindowsProcessesOutput = original })
	queryWindowsProcessesOutput = func(context.Context) ([]byte, error) {
		return []byte(`{"ProcessId":1,"Name":"WeChatAppEx-19027.exe"}`), nil
	}
	got, err := (TasklistFinder{}).FindDetailed(context.Background())
	if err != nil || len(got) != 1 || got[0].Version != 19027 {
		t.Fatalf("singleton=%+v err=%v", got, err)
	}
	queryWindowsProcessesOutput = func(context.Context) ([]byte, error) { return []byte("null"), nil }
	got, err = (TasklistFinder{}).FindDetailed(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("empty=%+v err=%v", got, err)
	}
}

func TestFindDetailedPropagatesQueryAndDecodeErrors(t *testing.T) {
	original := queryWindowsProcessesOutput
	t.Cleanup(func() { queryWindowsProcessesOutput = original })
	sentinel := errors.New("query failed")
	queryWindowsProcessesOutput = func(context.Context) ([]byte, error) { return nil, sentinel }
	if _, err := (TasklistFinder{}).FindDetailed(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("query error=%v", err)
	}
	queryWindowsProcessesOutput = func(context.Context) ([]byte, error) { return []byte("{"), nil }
	if _, err := (TasklistFinder{}).FindDetailed(context.Background()); err == nil {
		t.Fatal("malformed details unexpectedly succeeded")
	}
}
