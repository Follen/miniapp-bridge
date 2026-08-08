package wmpf

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type referenceMessageCase struct {
	Hex string `json:"protobuf_hex"`
}

type referenceFieldCase struct {
	Field string `json:"field"`
	Hex   string `json:"protobuf_hex"`
}

func TestReferenceGeneratedGoldenMessages(t *testing.T) {
	data, err := os.ReadFile("../../testdata/golden/reference_messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		ReferenceCommit string `json:"reference_commit"`
		Fixtures        []struct {
			Type         string               `json:"type"`
			Hex          string               `json:"protobuf_hex"`
			ExplicitZero referenceMessageCase `json:"explicit_zero"`
			Fields       []referenceFieldCase `json:"fields"`
		} `json:"fixtures"`
		CorruptInputs []struct {
			Name  string  `json:"name"`
			Hex   string  `json:"protobuf_hex"`
			Error *string `json:"error"`
		} `json:"corrupt_inputs"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.ReferenceCommit != "2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d" {
		t.Fatalf("reference commit=%s", golden.ReferenceCommit)
	}
	if len(golden.Fixtures) != 55 {
		t.Fatalf("fixture count=%d", len(golden.Fixtures))
	}
	if MessageTypeCount() != 55 {
		t.Fatalf("registered message count=%d", MessageTypeCount())
	}
	fieldCases := 0
	for _, fixture := range golden.Fixtures {
		raw, err := hex.DecodeString(fixture.Hex)
		if err != nil {
			t.Fatalf("%s: %v", fixture.Type, err)
		}
		message, err := DecodeGeneric(fixture.Type, raw)
		if err != nil {
			t.Fatalf("%s decode: %v", fixture.Type, err)
		}
		if encoded := EncodeGeneric(message); !bytes.Equal(encoded, raw) {
			t.Fatalf("%s re-encode=%x want=%x", fixture.Type, encoded, raw)
		}
		typed, ok := NewMessage(fixture.Type)
		if !ok {
			t.Fatalf("%s is not registered", fixture.Type)
		}
		if err := UnmarshalMessage(raw, typed); err != nil {
			t.Fatalf("%s typed decode: %v", fixture.Type, err)
		}
		encoded, err := MarshalMessage(typed)
		if err != nil {
			t.Fatalf("%s typed encode: %v", fixture.Type, err)
		}
		if !bytes.Equal(encoded, raw) {
			t.Fatalf("%s typed re-encode=%x want=%x", fixture.Type, encoded, raw)
		}

		zero := mustHex(t, fixture.ExplicitZero.Hex)
		genericZero, err := DecodeGeneric(fixture.Type, zero)
		if err != nil {
			t.Fatalf("%s explicit zero generic decode: %v", fixture.Type, err)
		}
		if encoded := EncodeGeneric(genericZero); !bytes.Equal(encoded, zero) {
			t.Fatalf("%s explicit zero generic re-encode=%x want=%x", fixture.Type, encoded, zero)
		}
		zeroTyped, _ := NewMessage(fixture.Type)
		if err := UnmarshalMessage(zero, zeroTyped); err != nil {
			t.Fatalf("%s explicit zero typed decode: %v", fixture.Type, err)
		}

		for _, field := range fixture.Fields {
			fieldCases++
			fieldRaw := mustHex(t, field.Hex)
			fieldTyped, _ := NewMessage(fixture.Type)
			if err := UnmarshalMessage(fieldRaw, fieldTyped); err != nil {
				t.Fatalf("%s.%s typed decode: %v", fixture.Type, field.Field, err)
			}
			fieldEncoded, err := MarshalMessage(fieldTyped)
			if err != nil {
				t.Fatalf("%s.%s typed encode: %v", fixture.Type, field.Field, err)
			}
			if !bytes.Equal(fieldEncoded, fieldRaw) {
				t.Fatalf("%s.%s typed re-encode=%x want=%x", fixture.Type, field.Field, fieldEncoded, fieldRaw)
			}
		}
	}
	if fieldCases != 131 {
		t.Fatalf("field fixture count=%d, want 131", fieldCases)
	}

	for _, fixture := range golden.CorruptInputs {
		t.Run("corrupt/"+fixture.Name, func(t *testing.T) {
			var ping PingProto
			err := UnmarshalMessage(mustHex(t, fixture.Hex), &ping)
			if fixture.Error != nil && err == nil {
				t.Fatalf("fixed reference rejected frame with %q, Go accepted it", *fixture.Error)
			}
			if fixture.Error == nil && err != nil {
				t.Logf("Go strictly rejected a frame accepted by fixed protobufjs: %v", err)
			}
		})
	}
}

func schemaSignature(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	messageRE := regexp.MustCompile(`(?s)message\s+(WARemoteDebug_\w+)\s*\{(.*?)\}`)
	fieldRE := regexp.MustCompile(`(?m)^\s*(repeated\s+)?([A-Za-z0-9_]+)\s+([A-Za-z0-9_]+)\s*=\s*([0-9]+)\s*;`)
	var signature []string
	for _, message := range messageRE.FindAllStringSubmatch(string(b), -1) {
		for _, field := range fieldRE.FindAllStringSubmatch(message[2], -1) {
			signature = append(signature, strings.Join([]string{message[1], strings.TrimSpace(field[1]), field[2], field[3], field[4]}, ":"))
		}
		if len(fieldRE.FindAllStringSubmatch(message[2], -1)) == 0 {
			signature = append(signature, message[1]+":<empty>")
		}
	}
	sort.Strings(signature)
	return signature
}

func TestRecoveredPublicSchemaMatchesRuntimeSchema(t *testing.T) {
	internal := schemaSignature(t, "schema.proto")
	public := schemaSignature(t, "../../proto/wmpf_remote_debug.proto")
	if !bytes.Equal([]byte(strings.Join(internal, "\n")), []byte(strings.Join(public, "\n"))) {
		t.Fatalf("public proto field signature differs from embedded runtime schema\ninternal=%v\npublic=%v", internal, public)
	}
}
