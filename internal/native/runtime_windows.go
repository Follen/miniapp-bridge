//go:build windows

package native

import (
	"debug/pe"
	"errors"
	"fmt"
)

func verifyPlatformFile(path string) error {
	f, err := pe.Open(path)
	if err != nil {
		return &Error{Code: ErrNativeWrongArch, Operation: "parse PE", Path: path, Err: err}
	}
	defer f.Close()
	if f.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return &Error{Code: ErrNativeWrongArch, Operation: "PE architecture", Path: path, Expected: "amd64", Actual: fmt.Sprintf("machine=0x%x", f.FileHeader.Machine), Err: errors.New("non-amd64 PE")}
	}
	return nil
}
