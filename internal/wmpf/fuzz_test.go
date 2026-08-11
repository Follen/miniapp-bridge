package wmpf

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

const fuzzInputLimit = 64 << 10

func FuzzWMPFDebugMessageDecode(f *testing.F) {
	plain := DebugMessage{
		Seq:      7,
		After:    3,
		Category: CategoryChromeDevtools,
		Data:     EncodeChrome(ChromeDevtools{OpID: 9, Payload: `{"id":1}`, JSContextID: "ctx"}),
	}
	f.Add(EncodeDebugMessage(plain))
	f.Add(append(EncodeDebugMessage(DebugMessage{Seq: 1}), 0x78, 0x01))
	f.Add([]byte{0x22, 0x05, 0x01})

	compressed, size, err := WrapData(plain.Data, plain.Category, CompressZlib)
	if err != nil {
		f.Fatal(err)
	}
	plain.Data = compressed
	plain.CompressAlgo = CompressZlib
	plain.OriginalSize = size
	f.Add(EncodeDebugMessage(plain))

	f.Fuzz(func(t *testing.T, wire []byte) {
		if len(wire) > fuzzInputLimit {
			t.Skip()
		}
		message, err := DecodeDebugMessage(wire)
		if err != nil {
			return
		}

		roundTrip, err := DecodeDebugMessage(EncodeDebugMessage(message))
		if err != nil {
			t.Fatalf("canonical message failed to decode: %v", err)
		}
		if message.Seq != roundTrip.Seq || message.After != roundTrip.After ||
			message.Category != roundTrip.Category || !bytes.Equal(message.Data, roundTrip.Data) ||
			message.CompressAlgo != roundTrip.CompressAlgo || message.OriginalSize != roundTrip.OriginalSize {
			t.Fatalf("semantic round trip changed message: before=%+v after=%+v", message, roundTrip)
		}

		// Arbitrary compressed input is expected to fail often. The invariant is
		// that the bounded decoder terminates without exceeding its output budget.
		unwrapped, unwrapErr := UnwrapDebugMessageWithLimit(message, fuzzInputLimit)
		if unwrapErr == nil {
			payload, ok := unwrapped.Data.([]byte)
			if !ok {
				t.Fatalf("successful unwrap returned %T", unwrapped.Data)
			}
			if len(payload) > fuzzInputLimit {
				t.Fatalf("unwrapped payload size=%d exceeds limit=%d", len(payload), fuzzInputLimit)
			}
		}
	})
}

func FuzzWMPFGenericDecode(f *testing.F) {
	const messageType = "WARemoteDebug_DebugMessage"
	f.Add(messageType, EncodeDebugMessage(DebugMessage{Seq: 1, Category: CategoryPing}))
	f.Add(messageType, []byte{0x22, 0x05, 0x01})
	f.Add("unknown", []byte{0x08, 0x01})

	f.Fuzz(func(t *testing.T, name string, wire []byte) {
		if len(name) > 128 || len(wire) > fuzzInputLimit {
			t.Skip()
		}
		message, err := DecodeGeneric(name, wire)
		if err != nil {
			return
		}
		canonical := EncodeGeneric(message)
		roundTrip, err := DecodeGeneric(name, canonical)
		if err != nil {
			t.Fatalf("canonical generic message failed to decode: %v", err)
		}
		if !bytes.Equal(canonical, EncodeGeneric(roundTrip)) {
			t.Fatal("generic message encoding was not stable")
		}
	})
}

func FuzzWMPFCommandDecode(f *testing.F) {
	payload, err := MarshalMessage(SendDebugMessageReq{
		BaseRequest:      &BaseReq{ClientVersion: 1},
		DebugMessageList: []DebugMessageProto{{Seq: 2, Category: CategoryPing}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint8(RoleDeveloper), CmdDevSendDebug, payload)
	f.Add(uint8(RoleClient), CmdClientHeartbeat, []byte{0x08, 0x01})
	f.Add(uint8(RoleClient), uint32(0), []byte{0xff})

	f.Fuzz(func(t *testing.T, roleByte uint8, cmd uint32, wire []byte) {
		if len(wire) > fuzzInputLimit {
			t.Skip()
		}
		role := EndpointRole(roleByte & 1)
		decoded, err := DecodeCommandPayload(role, cmd, wire)
		if err != nil {
			return
		}
		if decoded == nil {
			t.Fatalf("successful command decode returned nil for role=%d cmd=%d", role, cmd)
		}

		// Known typed payloads must remain decodable after canonical encoding.
		if reflect.TypeOf(decoded).Kind() == reflect.Map {
			return
		}
		canonical, err := MarshalMessage(decoded)
		if err != nil {
			t.Fatalf("marshal decoded command: %v", err)
		}
		if _, err := DecodeCommandPayload(role, cmd, canonical); err != nil {
			t.Fatalf("canonical command failed to decode: %v", err)
		}
	})
}

func FuzzWMPFZlibBoundedRoundTrip(f *testing.F) {
	f.Add([]byte(nil), uint32(0), 1)
	f.Add([]byte("zlib round trip"), uint32(15), 15)
	f.Add(bytes.Repeat([]byte("a"), 1024), uint32(1024), 1024)

	f.Fuzz(func(t *testing.T, input []byte, declared uint32, limit int) {
		if len(input) > fuzzInputLimit {
			t.Skip()
		}
		compressed, _, err := WrapData(input, CategoryChromeDevtools, CompressZlib)
		if err != nil {
			t.Fatalf("compress: %v", err)
		}

		// Exercise arbitrary declared sizes and limits first. Most combinations
		// reject by design; every successful result must honor the requested cap.
		boundedLimit := limit % (fuzzInputLimit + 1)
		output, _ := zlibDecompressBounded(compressed, declared, boundedLimit)
		if len(output) > boundedLimit && boundedLimit >= 0 {
			t.Fatalf("bounded output=%d exceeds limit=%d", len(output), boundedLimit)
		}

		roundTripLimit := len(input)
		if len(compressed) > roundTripLimit {
			roundTripLimit = len(compressed)
		}
		roundTrip, err := zlibDecompressBounded(compressed, uint32(len(input)), roundTripLimit)
		if err != nil {
			t.Fatalf("round trip decompress: %v", err)
		}
		if !bytes.Equal(roundTrip, input) {
			t.Fatalf("zlib round trip mismatch: got=%d want=%d", len(roundTrip), len(input))
		}
	})
}

func FuzzWMPFZlibDecode(f *testing.F) {
	valid, _, err := WrapData([]byte("direct zlib decode"), CategoryChromeDevtools, CompressZlib)
	if err != nil {
		f.Fatal(err)
	}
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-1] ^= 0xff
	f.Add(valid, uint32(len("direct zlib decode")), uint16(64))
	f.Add(valid[:len(valid)-1], uint32(0), uint16(64))
	f.Add(corrupt, uint32(0), uint16(64))
	f.Add([]byte("not-zlib"), uint32(0), uint16(64))

	bomb, _, err := WrapData(bytes.Repeat([]byte("a"), 64<<10), CategoryChromeDevtools, CompressZlib)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(bomb, uint32(0), uint16(math.MaxUint16))

	f.Fuzz(func(t *testing.T, compressed []byte, declared uint32, limit uint16) {
		if len(compressed) > fuzzInputLimit {
			t.Skip()
		}
		output, err := zlibDecompressBounded(compressed, declared, int(limit))
		if err != nil {
			return
		}
		if len(output) > int(limit) {
			t.Fatalf("decoded output=%d exceeds limit=%d", len(output), limit)
		}
		if declared != 0 && uint32(len(output)) != declared {
			t.Fatalf("decoded output=%d differs from declared=%d", len(output), declared)
		}
	})
}
