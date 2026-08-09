//go:build !windows

package native

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
