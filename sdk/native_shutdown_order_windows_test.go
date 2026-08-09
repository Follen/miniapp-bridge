//go:build windows && frida

package sdk

import (
	"context"
	"reflect"
	"testing"

	fridacore "github.com/Follen/miniapp-bridge/internal/frida"
	"github.com/Follen/miniapp-bridge/internal/process"
)

type nativeShutdownTrace struct{ events []string }

func (trace *nativeShutdownTrace) add(event string) { trace.events = append(trace.events, event) }

type orderedPlatformScript struct{ trace *nativeShutdownTrace }

func (script *orderedPlatformScript) Unload() error { script.trace.add("agent-unload"); return nil }
func (*orderedPlatformScript) Post([]byte) error    { return nil }

type orderedPlatformSession struct{ trace *nativeShutdownTrace }

func (*orderedPlatformSession) LoadScript(string) (fridacore.Script, error) { return nil, nil }
func (session *orderedPlatformSession) Detach() error {
	session.trace.add("session-detach")
	return nil
}

type orderedPlatformDevice struct{ trace *nativeShutdownTrace }

func (*orderedPlatformDevice) Attach(uint32) (fridacore.Session, error) { return nil, nil }
func (*orderedPlatformDevice) Enumerate(context.Context) ([]process.Process, error) {
	return nil, nil
}
func (*orderedPlatformDevice) SetMessageHandler(func(fridacore.Message)) {}
func (device *orderedPlatformDevice) Close() error {
	device.trace.add("device-close")
	device.trace.add("native-runtime-shutdown")
	device.trace.add("native-dll-unload")
	return nil
}

func TestPlatformNativeShutdownOrderIsExactAndIdempotent(t *testing.T) {
	trace := &nativeShutdownTrace{}
	native := &platformNativeSession{
		device:  &orderedPlatformDevice{trace: trace},
		session: &orderedPlatformSession{trace: trace},
		script:  &orderedPlatformScript{trace: trace},
	}
	if err := native.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"agent-unload", "session-detach", "device-close", "native-runtime-shutdown", "native-dll-unload"}
	if !reflect.DeepEqual(trace.events, want) {
		t.Fatalf("native shutdown order=%v want=%v", trace.events, want)
	}
}
