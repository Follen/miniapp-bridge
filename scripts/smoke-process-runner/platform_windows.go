//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	ctrlBreakEvent        = 1
)

var generateConsoleCtrlEvent = syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
var commandFactory = exec.Command
var callGenerateConsoleCtrlEvent = func(event, processGroup uintptr) (uintptr, uintptr, error) {
	return generateConsoleCtrlEvent.Call(event, processGroup)
}

type systemChild struct{ command *exec.Cmd }

func (child *systemChild) PID() int    { return child.command.Process.Pid }
func (child *systemChild) Wait() error { return child.command.Wait() }
func (child *systemChild) Kill() error { return child.command.Process.Kill() }

func systemDependencies() dependencies {
	return dependencies{
		start:          startSystemChild,
		stopFileExists: fileExists,
		generateBreak:  sendCtrlBreak,
		stdout:         os.Stdout,
	}
}

func startSystemChild(config Config) (childProcess, error) {
	command := commandFactory(config.Executable, config.Args...)
	command.Dir = config.WorkDir
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &systemChild{command: command}, nil
}

func sendCtrlBreak(processGroupID uint32) error {
	result, _, callErr := callGenerateConsoleCtrlEvent(ctrlBreakEvent, uintptr(processGroupID))
	if result == 0 {
		return fmt.Errorf("GenerateConsoleCtrlEvent: %w", callErr)
	}
	return nil
}
