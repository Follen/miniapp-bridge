//go:build windows

package native

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
	lockFileFailImmediately = 0x1
	lockFileExclusiveLock   = 0x2
	errorLockViolation      = syscall.Errno(33)
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	moveFileExW    = kernel32.NewProc("MoveFileExW")
	lockFileEx     = kernel32.NewProc("LockFileEx")
	unlockFileEx   = kernel32.NewProc("UnlockFileEx")
	lockFileExCall = func(args ...uintptr) (uintptr, uintptr, error) {
		return lockFileEx.Call(args...)
	}
	unlockFileExCall = func(args ...uintptr) (uintptr, uintptr, error) {
		return unlockFileEx.Call(args...)
	}
)

func replaceFileAtomic(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	ok, _, callErr := moveFileExW.Call(
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(to)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if ok == 0 {
		return callErr
	}
	return nil
}

func tryLockFile(file *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	ok, _, callErr := lockFileExCall(
		file.Fd(),
		lockFileExclusiveLock|lockFileFailImmediately,
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ok != 0 {
		return true, nil
	}
	if errors.Is(callErr, errorLockViolation) {
		return false, nil
	}
	return false, callErr
}

func unlockFile(file *os.File) error {
	var overlapped syscall.Overlapped
	ok, _, callErr := unlockFileExCall(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ok == 0 {
		return callErr
	}
	return nil
}
