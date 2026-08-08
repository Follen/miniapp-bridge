//go:build !windows

package main

import (
	"errors"
	"os"
)

func systemDependencies() dependencies {
	return dependencies{
		start:          func(Config) (childProcess, error) { return nil, errors.New("Windows is required") },
		stopFileExists: fileExists,
		generateBreak:  func(uint32) error { return errors.New("Windows is required") },
		stdout:         os.Stdout,
	}
}
