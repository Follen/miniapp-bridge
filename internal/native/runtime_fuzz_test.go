package native

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"runtime"
	"testing"
)

const manifestFuzzInputLimit = 64 << 10

func fuzzPlatformManifest() Manifest {
	manifest := DefaultManifest()
	manifest.OS = runtime.GOOS
	manifest.Arch = runtime.GOARCH
	return manifest
}

func FuzzNativeManifestDecode(f *testing.F) {
	valid, err := json.Marshal(fuzzPlatformManifest())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"one","schema":"two"}`))
	f.Add(append(append([]byte(nil), valid...), []byte(` {}`)...))
	duplicateExports := fuzzPlatformManifest()
	duplicateExports.RequiredExports = append(duplicateExports.RequiredExports, duplicateExports.RequiredExports[0])
	duplicateJSON, err := json.Marshal(duplicateExports)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(duplicateJSON)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > manifestFuzzInputLimit {
			t.Skip()
		}
		manifest, err := decodeManifest(data)
		if err != nil {
			return
		}

		canonical, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal decoded manifest: %v", err)
		}
		roundTrip, err := decodeManifest(canonical)
		if err != nil {
			t.Fatalf("canonical manifest failed to decode: %v", err)
		}
		if !reflect.DeepEqual(manifest, roundTrip) {
			t.Fatalf("manifest changed after canonical round trip: before=%+v after=%+v", manifest, roundTrip)
		}
		_ = validateManifest(manifest, "fuzz-manifest.json")
	})
}

func FuzzNativeManifestTrustBoundaries(f *testing.F) {
	validSHA := fuzzPlatformManifest().SHA256
	f.Add(int64(1), validSHA)
	f.Add(defaultDLLLimit, validSHA)
	f.Add(defaultDLLLimit+1, validSHA)
	f.Add(int64(0), validSHA)
	f.Add(int64(-1), validSHA)
	f.Add(int64(math.MaxInt64), validSHA)
	f.Add(int64(math.MinInt64), validSHA)
	f.Add(int64(1), "")
	f.Add(int64(1), validSHA[:len(validSHA)-1]+"g")

	f.Fuzz(func(t *testing.T, size int64, sha256 string) {
		if len(sha256) > 128 {
			t.Skip()
		}
		manifest := fuzzPlatformManifest()
		manifest.Size = size
		manifest.SHA256 = sha256
		err := validateManifest(manifest, "fuzz-manifest.json")
		trusted := size > 0 && size <= defaultDLLLimit && validSHA256(sha256)
		if trusted && err != nil {
			t.Fatalf("valid trust fields rejected: size=%d sha256=%q err=%v", size, sha256, err)
		}
		if !trusted && !errors.Is(err, ErrNativeManifest) {
			t.Fatalf("invalid trust fields accepted: size=%d sha256=%q err=%v", size, sha256, err)
		}
	})
}
