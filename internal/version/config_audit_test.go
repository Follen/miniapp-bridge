package version

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestAuditAddressConfigSetAndFields(t *testing.T) {
	t.Parallel()
	configs, err := LoadDir("../../configs/addresses")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{
		11581, 11633, 13331, 13341, 13487, 13639, 13655, 13871, 13909,
		14161, 14199, 14315, 16133, 16203, 16389, 16467, 16771, 16815,
		16965, 17037, 17071, 17127, 18055, 18151, 18787, 18891, 18955,
		19027, 19201, 19339, 19459, 19481, 19749, 19769, 19823, 19841,
		19871, 19881, 19899, 19921, 19977, 20001, 20005, 20079, 20089,
		25268, 25297,
	}
	got := make([]int, 0, len(configs))
	for version, config := range configs {
		got = append(got, version)
		if config.Version != version {
			t.Errorf("map key %d contains Version %d", version, config.Version)
		}
		if _, err := config.Offset(config.LoadStartHookOffset); err != nil {
			t.Errorf("version %d LoadStartHookOffset: %v", version, err)
		}
		if _, err := config.Offset(config.CDPFilterHookOffset); err != nil {
			t.Errorf("version %d CDPFilterHookOffset: %v", version, err)
		}
		if len(config.SceneOffsets) != 6 {
			t.Errorf("version %d SceneOffsets length=%d, want 6", version, len(config.SceneOffsets))
		}
	}
	sort.Ints(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("address version set mismatch\ngot:  %v\nwant: %v", got, want)
	}
}

func TestAuditAddressConfigBytesMatchPinnedReferenceManifest(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../configs/addresses/addresses.*.json")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var manifest strings.Builder
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		manifest.WriteString(filepath.Base(path))
		manifest.WriteByte(':')
		manifest.WriteString(strings.ToUpper(hex.EncodeToString(sum[:])))
		manifest.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(manifest.String()))
	const want = "0FF90920BDC3E679E76B176A805E4F9A735A43703D97A9AA040E7923373E2672"
	if got := strings.ToUpper(hex.EncodeToString(sum[:])); got != want {
		t.Fatalf("address config aggregate SHA-256=%s, want %s", got, want)
	}
}

func TestAuditSelectRejectsNearestVersionFallback(t *testing.T) {
	t.Parallel()
	configs := map[int]AddressConfig{19027: {Version: 19027, SceneOffsets: []int{1, 2, 3, 4, 5, 6}}}
	if _, err := Select(configs, 19028); err == nil {
		t.Fatal("Select accepted a non-exact version")
	}
}
