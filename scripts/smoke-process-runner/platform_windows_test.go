//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"testing"
	"time"
)

func TestSystemDependenciesAndChildLifecycle(t *testing.T) {
	deps := systemDependencies()
	if deps.start == nil || deps.stopFileExists == nil || deps.generateBreak == nil || deps.stdout != os.Stdout {
		t.Fatal("incomplete dependencies")
	}
	child, err := startSystemChild(Config{Executable: "cmd.exe", Args: []string{"/c", "exit", "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if child.PID() <= 0 {
		t.Fatalf("pid=%d", child.PID())
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}

	sleeper, err := startSystemChild(Config{Executable: "cmd.exe", Args: []string{"/c", "ping", "-n", "30", "127.0.0.1", ">nul"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sleeper.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = sleeper.Wait()
	if _, err := startSystemChild(Config{Executable: `Z:\definitely-missing\runner.exe`}); err == nil {
		t.Fatal("expected start error")
	}
}

func TestSendCtrlBreak(t *testing.T) {
	original := callGenerateConsoleCtrlEvent
	defer func() { callGenerateConsoleCtrlEvent = original }()
	callGenerateConsoleCtrlEvent = func(event, group uintptr) (uintptr, uintptr, error) {
		if event != ctrlBreakEvent || group != 123 {
			t.Fatalf("event=%d group=%d", event, group)
		}
		return 1, 0, errors.New("ignored")
	}
	if err := sendCtrlBreak(123); err != nil {
		t.Fatal(err)
	}
	callGenerateConsoleCtrlEvent = func(uintptr, uintptr) (uintptr, uintptr, error) { return 0, 0, errors.New("win32 failure") }
	if err := sendCtrlBreak(123); err == nil || !strings.Contains(err.Error(), "win32 failure") {
		t.Fatalf("err=%v", err)
	}
}

func TestCommandFactorySeam(t *testing.T) {
	original := commandFactory
	defer func() { commandFactory = original }()
	commandFactory = func(string, ...string) *exec.Cmd { return exec.Command("cmd.exe", "/c", "exit", "0") }
	child, err := startSystemChild(Config{Executable: "ignored", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCtrlBreakIntegration(t *testing.T) {
	ready := t.TempDir() + `\ready`
	received := t.TempDir() + `\received`
	child, err := startSystemChild(Config{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestCtrlBreakHelper$", "--", "ctrl-break-helper", ready, received},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Kill()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := sendCtrlBreak(uint32(child.PID())); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("helper exit: %v", err)
	}
	if data, err := os.ReadFile(received); err != nil || string(data) != "interrupt" {
		t.Fatalf("received=%q err=%v", data, err)
	}
}

func TestCtrlBreakHelper(t *testing.T) {
	marker := -1
	for index, arg := range os.Args {
		if arg == "ctrl-break-helper" {
			marker = index
			break
		}
	}
	if marker < 0 {
		return
	}
	if marker+2 >= len(os.Args) {
		t.Fatal("missing helper paths")
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	if err := os.WriteFile(os.Args[marker+1], []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-interrupt:
		if err := os.WriteFile(os.Args[marker+2], []byte("interrupt"), 0o600); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CTRL_BREAK was not delivered as os.Interrupt")
	}
}
