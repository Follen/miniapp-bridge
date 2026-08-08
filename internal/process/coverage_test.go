package process

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTasklistFinderBranches(t *testing.T) {
	original := tasklistOutput
	t.Cleanup(func() { tasklistOutput = original })

	sentinel := errors.New("tasklist failed")
	tasklistOutput = func(context.Context) ([]byte, error) { return nil, sentinel }
	if _, err := (TasklistFinder{}).Find(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("error=%v want=%v", err, sentinel)
	}

	tasklistOutput = func(context.Context) ([]byte, error) {
		return []byte("malformed\n\"Other.exe\",\"10\"\r\n\"WeChatAppEx-19027.exe\",\"bad\"\r\n\"WeChatAppEx-19027.exe\",\"42\"\r\n"), nil
	}
	got, err := (TasklistFinder{Names: []string{"wechatappex-19027.EXE"}}).Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{{PID: 42, Name: "WeChatAppEx-19027.exe", Version: 19027}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("processes=%+v want=%+v", got, want)
	}

	got, err = (TasklistFinder{}).Find(context.Background())
	if err != nil || len(got) != 2 || got[0].Name != "Other.exe" {
		t.Fatalf("unfiltered=%+v err=%v", got, err)
	}
}

func TestParseVersionAndSelectTargetBranches(t *testing.T) {
	if got := ParseVersion("no-version"); got != 0 {
		t.Fatalf("ParseVersion=%d", got)
	}
	processes := []Process{{PID: 1, Name: "other.exe"}, {PID: 2, Name: "TARGET.EXE"}}
	got, err := SelectTarget(processes, "missing.exe", "target.exe")
	if err != nil || got.PID != 2 {
		t.Fatalf("target=%+v err=%v", got, err)
	}
	if _, err := SelectTarget(processes, "missing.exe"); err == nil {
		t.Fatal("expected missing target error")
	}
}
