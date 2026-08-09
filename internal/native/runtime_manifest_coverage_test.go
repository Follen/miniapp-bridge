package native

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestVerifyManifestRejectsSchemaAndExportMismatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), NativeDLLFileName)

	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{
			name: "schema",
			mutate: func(manifest *Manifest) {
				manifest.Schema = "miniapp-bridge.native-manifest.invalid"
			},
		},
		{
			name: "exports",
			mutate: func(manifest *Manifest) {
				manifest.RequiredExports = append([]string(nil), manifest.RequiredExports...)
				manifest.RequiredExports[0] = "mb_unknown_export"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := currentPlatformManifest()
			test.mutate(&manifest)
			if err := VerifyManifest(path, manifest); !errors.Is(err, ErrNativeManifest) {
				t.Fatalf("VerifyManifest() error = %v, want ErrNativeManifest", err)
			}
		})
	}
}

func TestSameStringSetRejectsLengthAndMembershipMismatches(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{name: "length", got: []string{"one"}, want: []string{"one", "two"}},
		{name: "membership", got: []string{"one", "one"}, want: []string{"one", "two"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if sameStringSet(test.got, test.want) {
				t.Fatalf("sameStringSet(%v, %v) = true, want false", test.got, test.want)
			}
		})
	}
}

func TestPrepareDefaultsManifestBeforeCacheLookup(t *testing.T) {
	original := nativeUserCacheDir
	t.Cleanup(func() { nativeUserCacheDir = original })
	nativeUserCacheDir = func() (string, error) { return "", errors.New("cache unavailable") }
	if _, err := Prepare(context.Background(), PrepareOptions{}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("Prepare() error = %v, want ErrNativeCache", err)
	}
}
