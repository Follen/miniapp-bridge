package wmpf

import (
	"bytes"
	"testing"
)

func TestUnknownFieldsSurviveRoundTrip(t *testing.T) {
	known, err := MarshalMessage(PingProto{PingId: 7, Payload: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := make([]byte, 0)
	putVarBytes(&unknown, uint64(90<<3|0))
	putVarBytes(&unknown, 1234)
	putVarBytes(&unknown, uint64(91<<3|1))
	unknown = append(unknown, 1, 2, 3, 4, 5, 6, 7, 8)
	putVarBytes(&unknown, uint64(92<<3|2))
	putVarBytes(&unknown, 3)
	unknown = append(unknown, 'r', 'a', 'w')
	putVarBytes(&unknown, uint64(93<<3|5))
	unknown = append(unknown, 9, 10, 11, 12)
	wire := append(append([]byte(nil), known...), unknown...)

	var decoded PingProto
	if err := UnmarshalMessage(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.UnknownFields, unknown) {
		t.Fatalf("unknown=%x want %x", decoded.UnknownFields, unknown)
	}
	roundTrip, err := MarshalMessage(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, wire) {
		t.Fatalf("round trip=%x want %x", roundTrip, wire)
	}
}

func TestTruncatedUnknownFieldFails(t *testing.T) {
	var decoded PingProto
	if err := UnmarshalMessage([]byte{0xdd, 0x05, 1, 2}, &decoded); err == nil {
		t.Fatal("truncated fixed32 field decoded without error")
	}
}
