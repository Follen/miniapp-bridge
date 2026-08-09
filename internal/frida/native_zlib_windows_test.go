//go:build windows && frida

package frida

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRuntimeRetainAndZlibRoundTrip(t *testing.T) {
	releaseFirst, err := retainNativeRuntime()
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := retainNativeRuntime()
	if err != nil {
		releaseFirst()
		t.Fatal(err)
	}
	releaseSecond()
	releaseFirst()

	for _, input := range [][]byte{nil, []byte("native zlib round trip")} {
		compressed, err := ZlibCompress(input)
		if err != nil {
			t.Fatalf("compress %q: %v", input, err)
		}
		decompressed, err := ZlibDecompress(compressed, len(input))
		if err != nil {
			t.Fatalf("decompress %q: %v", input, err)
		}
		if !bytes.Equal(decompressed, input) {
			t.Fatalf("round trip=%q want=%q", decompressed, input)
		}
	}
}

func TestNativeZlibInputAndValidationErrors(t *testing.T) {
	input, freeInput := nativeZlibInput(nil)
	if input != nil {
		t.Fatal("empty native input is non-nil")
	}
	freeInput()
	compressed, err := ZlibCompress([]byte("expected-size"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ZlibDecompress(compressed, len("expected-size")+1); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected-size err=%v", err)
	}
	input, freeInput = nativeZlibInput([]byte("x"))
	if input == nil {
		t.Fatal("non-empty native input is nil")
	}
	freeInput()

	for name, run := range map[string]func() error{
		"corrupt":           func() error { _, err := ZlibDecompress([]byte("not-zlib"), 0); return err },
		"empty compressed":  func() error { _, err := ZlibDecompress(nil, 0); return err },
		"negative expected": func() error { _, err := ZlibDecompress(nil, -1); return err },
		"large expected":    func() error { _, err := ZlibDecompress(nil, maxNativeZlibOutput+1); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("expected zlib error")
			}
		})
	}

}

func TestNativeZlibFakeDLLFailuresAndOutputLimits(t *testing.T) {
	originalPath := nativeRuntimePath
	t.Cleanup(func() { nativeRuntimePath = originalPath })

	failure := buildNativeZlibFixture(t, "compress-failure", fakeCompressFailure, fakeDecompressCopy)
	nativeRuntimePath = func() (string, error) { return failure, nil }
	if _, err := ZlibCompress([]byte("failure")); err == nil || !strings.Contains(err.Error(), "zlib compress") {
		t.Fatalf("compress failure err=%v", err)
	}

	oversize := buildNativeZlibFixture(t, "oversize", fakeCompressOversize, fakeDecompressOversize)
	nativeRuntimePath = func() (string, error) { return oversize, nil }
	if _, err := ZlibCompress([]byte("oversize")); err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("compress oversize err=%v", err)
	}
	if _, err := ZlibDecompress([]byte("oversize"), 0); err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("decompress oversize err=%v", err)
	}
}

func buildNativeZlibFixture(t *testing.T, name, compress, decompress string) string {
	t.Helper()
	const scriptPost = `__declspec(dllexport) int mb_script_post(mb_script *s,const char *j,char **e){(void)s;(void)j;(void)e;return 1;}`
	source := fmt.Sprintf(loaderFixtureTemplate, "1.3.1", scriptPost)
	source = strings.Replace(source, fakeCompressCopy, compress, 1)
	source = strings.Replace(source, fakeDecompressCopy, decompress, 1)
	path := filepath.Join(t.TempDir(), name+".c")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	runMSVCCommand(t, dir, `C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\Tools\VsDevCmd.bat`, fmt.Sprintf(`cl /nologo /LD /MT /W3 /Fe:"%s.dll" "%s.c"`, name, name))
	return filepath.Join(dir, name+".dll")
}

const fakeCompressCopy = `__declspec(dllexport) int mb_zlib_compress(const uint8_t*i,size_t n,uint8_t**o,size_t*s,char**e){(void)e;*o=(uint8_t*)malloc(n?n:1);if(!*o)return 0;if(n)memcpy(*o,i,n);*s=n;return 1;}`
const fakeDecompressCopy = `__declspec(dllexport) int mb_zlib_decompress(const uint8_t*i,size_t n,size_t x,size_t m,uint8_t**o,size_t*s,char**e){(void)x;(void)m;return mb_zlib_compress(i,n,o,s,e);}`
const fakeCompressFailure = `__declspec(dllexport) int mb_zlib_compress(const uint8_t*i,size_t n,uint8_t**o,size_t*s,char**e){(void)i;(void)n;(void)e;*o=NULL;*s=0;return 0;}`
const fakeCompressOversize = `__declspec(dllexport) int mb_zlib_compress(const uint8_t*i,size_t n,uint8_t**o,size_t*s,char**e){(void)i;(void)n;(void)e;*o=(uint8_t*)malloc(1);if(!*o)return 0;*s=268435457;return 1;}`
const fakeDecompressOversize = `__declspec(dllexport) int mb_zlib_decompress(const uint8_t*i,size_t n,size_t x,size_t m,uint8_t**o,size_t*s,char**e){(void)i;(void)n;(void)x;(void)m;(void)e;*o=(uint8_t*)malloc(1);if(!*o)return 0;*s=268435457;return 1;}`

func TestNativeMessageQueueReceivesBeforeStop(t *testing.T) {
	received := make(chan Message, 1)
	device := &NativeDevice{
		handler:      func(message Message) { received <- message },
		messageQueue: make(chan Message, 1),
		messageStop:  make(chan struct{}),
		messageDone:  make(chan struct{}),
	}
	go device.runMessageQueue()
	device.messageQueue <- Message{Type: "normal"}
	if message := <-received; message.Type != "normal" {
		t.Fatalf("message=%+v", message)
	}
	close(device.messageStop)
	<-device.messageDone
}

func TestNativeZlibRuntimeLoadError(t *testing.T) {
	originalPath := nativeRuntimePath
	t.Cleanup(func() { nativeRuntimePath = originalPath })
	nativeRuntimePath = func() (string, error) { return filepath.Join(t.TempDir(), "missing.dll"), nil }
	if _, err := ZlibCompress([]byte("load")); err == nil || !strings.Contains(err.Error(), "native runtime") {
		t.Fatalf("compress runtime err=%v", err)
	}
	if _, err := ZlibDecompress([]byte("load"), 0); err == nil || !strings.Contains(err.Error(), "native runtime") {
		t.Fatalf("decompress runtime err=%v", err)
	}
}
