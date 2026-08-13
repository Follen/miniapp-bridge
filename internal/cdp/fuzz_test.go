package cdp

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

const cdpFuzzInputLimit = 4096

func FuzzCDPJSONNumberCanonicalization(f *testing.F) {
	for _, seed := range []string{
		"0", "-0", "1", "1.0", "1e0", "-42.500e+3", "18446744073709551615",
		"9999999999999999999999999999999999999999999999999999999999999999",
		"", "01", "1.", "1e", "NaN",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, encoded string) {
		if len(encoded) > cdpFuzzInputLimit {
			t.Skip()
		}
		marshaled, marshalErr := json.Marshal(json.Number(encoded))
		canonical, valid := "", false
		if marshalErr == nil {
			canonical, valid = canonicalJSONNumber(string(marshaled))
		}
		key, keyValid := IDKey(json.Number(encoded))
		if valid {
			if !keyValid || !strings.HasPrefix(key, "n:") || strings.TrimPrefix(key, "n:") != canonical {
				t.Fatalf("canonical=%q valid=%t key=%q keyValid=%t", canonical, valid, key, keyValid)
			}
			second, secondValid := canonicalJSONNumber(canonical)
			if !secondValid || second != canonical {
				t.Fatalf("canonical form is not idempotent: first=%q second=%q", canonical, second)
			}
			return
		}
		if keyValid {
			t.Fatalf("unmarshalable JSON number accepted by IDKey: %q -> %q", encoded, key)
		}
	})
}

func FuzzCDPIDKeyTypeBoundaries(f *testing.F) {
	f.Add(uint64(0), int64(0), "")
	f.Add(uint64(math.MaxUint64), int64(math.MinInt64), "boundary")
	f.Add(uint64(math.MaxInt64), int64(math.MaxInt64), strings.Repeat("x", 128))

	f.Fuzz(func(t *testing.T, unsigned uint64, signed int64, text string) {
		if len(text) > cdpFuzzInputLimit {
			t.Skip()
		}
		values := []any{unsigned, signed, json.Number(text), text, nil}
		for _, value := range values {
			first, firstNumeric := IDKey(value)
			second, secondNumeric := IDKey(value)
			if first != second || firstNumeric != secondNumeric {
				t.Fatalf("IDKey is not deterministic for %T: %q/%q %t/%t", value, first, second, firstNumeric, secondNumeric)
			}
		}
	})
}
