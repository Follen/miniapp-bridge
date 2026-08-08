package wmpf

import (
	"bytes"
	"testing"
)

// These bytes come from RemoteDebugCodex.wrapDebugMessageData at the fixed
// reference commit. The wrapper creates own-properties even for scalar zero
// values, so protobufjs emits those fields instead of treating them as absent.
func TestAuditReferenceCategoryEncodersPreserveExplicitZeroFields(t *testing.T) {
	tests := []struct {
		name     string
		category string
		value    any
		want     []byte
	}{
		{"breakpoint false", CategoryBreakpoint, Breakpoint{}, []byte{0x08, 0x00}},
		{"ping zero", CategoryPing, Ping{}, []byte{0x08, 0x00, 0x12, 0x00}},
		{"call interface zero", CategoryCallInterface, CallInterface{}, []byte{0x0a, 0x00, 0x12, 0x00, 0x20, 0x00}},
		{"evaluate result zero", CategoryEvaluateJavascriptResult, EvaluateJavascriptResult{}, []byte{0x0a, 0x00, 0x10, 0x00}},
		{"chrome zero", CategoryChromeDevtools, ChromeDevtools{}, []byte{0x08, 0x00, 0x12, 0x00, 0x1a, 0x00}},
		{"custom zero", CategoryCustomMessage, CustomMessage{}, []byte{0x0a, 0x00, 0x12, 0x00}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EncodeCategory(tc.category, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("encoded %x, fixed reference %x", got, tc.want)
			}
		})
	}
}

// protobufjs' generated decoder assigns a freshly decoded child for each
// occurrence of a singular message field. Consequently the last occurrence
// replaces, rather than merges with, the previous child.
func TestAuditReferenceDuplicateSingularMessageLastOccurrenceReplaces(t *testing.T) {
	// SetupContext.registerInterface={objName:"first"}, followed by an
	// explicitly empty registerInterface. The fixed reference yields objName="".
	wire := []byte{0x0a, 0x07, 0x0a, 0x05, 'f', 'i', 'r', 's', 't', 0x0a, 0x00}
	var got SetupContext
	if err := UnmarshalMessage(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.RegisterInterface == nil {
		t.Fatal("registerInterface is nil; fixed reference returns an empty child")
	}
	if got.RegisterInterface.ObjName != "" {
		t.Fatalf("objName=%q, fixed reference last occurrence replaces it with empty", got.RegisterInterface.ObjName)
	}
}

func TestAuditRecoveredSchemaContainsCallInterfaceArgumentField(t *testing.T) {
	// WARemoteDebugProtobuf.js lines 8911-8913 encode methodArgList as repeated
	// string field 3. A complete recovered schema therefore has four fields.
	if got := SchemaFieldCount("WARemoteDebug_CallInterface"); got != 4 {
		t.Fatalf("schema field count=%d, fixed reference has objName=1, methodName=2, methodArgList=3, callId=4", got)
	}
}

func TestAuditReferenceClientUnwrapUnsupportedCommandsReturnEmptyObject(t *testing.T) {
	// unwrapClientProtoToDataFormat has no switch arms for these numeric client
	// commands at the fixed reference commit. Its observable result is data={}
	// without a decode error, even when the command exists in another direction.
	commands := []uint32{
		CmdClientSendDebug,
		CmdClientMessageNotify,
		CmdClientMessageNotifyParallel,
		CmdEventBegin,
		CmdEventEnd,
		CmdEventBlock,
	}
	for _, cmd := range commands {
		t.Run(string(rune(cmd)), func(t *testing.T) {
			got, err := DecodeCommandPayload(RoleClient, cmd, nil)
			if err != nil {
				t.Fatalf("cmd %d returned error %v; fixed reference returns data={}", cmd, err)
			}
			empty, ok := got.(map[string]any)
			if !ok || len(empty) != 0 {
				t.Fatalf("cmd %d decoded as %T (%v), fixed reference returns data={}", cmd, got, got)
			}
		})
	}
}

func TestAuditCorruptZlibFrameReturnsErrorAndPreservesRaw(t *testing.T) {
	raw := []byte{0x78, 0x9c, 0x00, 0xff, 0x00}
	unwrapped, err := UnwrapDebugMessage(DebugMessage{Category: CategoryPing, Data: raw, CompressAlgo: CompressZlib})
	if err == nil {
		t.Fatal("corrupt zlib frame unexpectedly decoded")
	}
	if !bytes.Equal(unwrapped.Raw, raw) {
		t.Fatalf("raw frame=%x, want %x", unwrapped.Raw, raw)
	}
}
