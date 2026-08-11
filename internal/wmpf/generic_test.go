package wmpf

import (
	"encoding/hex"
	"testing"
)

func TestRecoveredSchemaContainsAllMessages(t *testing.T) {
	types := MessageTypes()
	if len(types) != 55 {
		t.Fatalf("message count=%d", len(types))
	}
	for _, name := range types {
		m, e := DecodeGeneric(name, nil)
		if e != nil {
			t.Fatal(name, e)
		}
		if EncodeGeneric(m) == nil && SchemaFieldCount(name) > 0 {
			t.Fatalf("empty encoding for %s", name)
		}
	}
}
func TestGenericRoundtripNestedRepeatedUnknown(t *testing.T) {
	raw, _ := hex.DecodeString("0a030102031201aa")
	m, e := DecodeGeneric("WARemoteDebug_SetupContext", raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Fields) != 2 || string(EncodeGeneric(m)) != string(raw) {
		t.Fatalf("fields=%d roundtrip mismatch", len(m.Fields))
	}
	if m.Fields[0].Number != 1 || m.Fields[1].Number != 2 {
		t.Fatal(m.Fields)
	}
}
func TestGenericCorrupt(t *testing.T) {
	if _, e := DecodeGeneric("WARemoteDebug_DebugMessage", []byte{0x22, 0x05, 0x01}); e == nil {
		t.Fatal("expected truncated error")
	}
}
