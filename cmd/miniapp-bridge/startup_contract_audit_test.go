package main

import (
	"os"
	"strings"
	"testing"
)

func TestAuditMainStartsListenersBeforeNativeAttach(t *testing.T) {
	t.Parallel()
	source := readMainAuditSource(t, "main.go")
	listener := strings.Index(source, "a.Start()")
	native := strings.Index(source, "startNative(")
	if listener < 0 || native < 0 || listener >= native {
		t.Fatalf("listener index=%d native index=%d", listener, native)
	}
}

func TestAuditMainClosesAppBeforeNativeDetach(t *testing.T) {
	t.Parallel()
	source := readMainAuditSource(t, "main.go")
	signalIndex := strings.Index(source, "<-sig")
	appCloseIndex := strings.Index(source[signalIndex:], "a.Close(context.Background())")
	nativeCloseIndex := strings.Index(source[signalIndex:], "closeNative()")
	if signalIndex < 0 || appCloseIndex < 0 || nativeCloseIndex < 0 {
		t.Fatalf("shutdown calls not found: signal=%d app=%d native=%d", signalIndex, appCloseIndex, nativeCloseIndex)
	}
	if appCloseIndex >= nativeCloseIndex {
		t.Fatalf("shutdown must close App before native detach: app=%d native=%d", appCloseIndex, nativeCloseIndex)
	}
}

func TestAuditWindowsShutdownOrderAndFailureRelease(t *testing.T) {
	t.Parallel()
	source := readMainAuditSource(t, "native_windows.go")
	successStart := strings.Index(source, "return func() error")
	if successStart < 0 {
		t.Fatal("shutdown closure not found")
	}
	attachFailureStart := strings.Index(source, "session, script, target, err := bootstrap.Attach(ctx)")
	if attachFailureStart < 0 || attachFailureStart >= successStart {
		t.Fatal("attach failure branch not found")
	}
	for _, candidate := range []struct {
		text    string
		ordered []string
	}{
		{source[successStart:], []string{"script.Unload()", "session.Detach()", "device.Close()", "fridacore.ShutdownRuntime()"}},
		{source[attachFailureStart:successStart], []string{"_ = device.Close()", "fridacore.ShutdownRuntime()", "return nil, err"}},
	} {
		last := -1
		for _, token := range candidate.ordered {
			index := strings.Index(candidate.text, token)
			if index < 0 || index <= last {
				t.Fatalf("shutdown token %q index=%d after=%d", token, index, last)
			}
			last = index
		}
	}
}

func readMainAuditSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
