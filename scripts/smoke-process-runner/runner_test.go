package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeChild struct {
	pid     int
	wait    chan error
	mu      sync.Mutex
	killed  bool
	killErr error
}

func (child *fakeChild) PID() int    { return child.pid }
func (child *fakeChild) Wait() error { return <-child.wait }
func (child *fakeChild) Kill() error {
	child.mu.Lock()
	defer child.mu.Unlock()
	child.killed = true
	if child.killErr == nil {
		select {
		case child.wait <- errors.New("killed"):
		default:
		}
	}
	return child.killErr
}

func TestRunGracefulStop(t *testing.T) {
	child := &fakeChild{pid: 42, wait: make(chan error, 1)}
	var output bytes.Buffer
	stopChecks := 0
	err := Run(context.Background(), validConfig(), dependencies{
		start:          func(Config) (childProcess, error) { return child, nil },
		stopFileExists: func(string) (bool, error) { stopChecks++; return stopChecks > 1, nil },
		generateBreak: func(pid uint32) error {
			if pid != 42 {
				t.Fatalf("pid=%d", pid)
			}
			child.wait <- nil
			return nil
		},
		stdout: &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"child-pid=42", "stop-requested=true", "child-exit-status=0"} {
		if !strings.Contains(output.String(), token) {
			t.Fatalf("output=%q missing %q", output.String(), token)
		}
	}
	if !strings.Contains(output.String(), "child-exit-code=0") {
		t.Fatalf("output=%q missing exit code", output.String())
	}
}

func TestRunNaturalExit(t *testing.T) {
	child := &fakeChild{pid: 7, wait: make(chan error, 1)}
	child.wait <- nil
	err := Run(context.Background(), validConfig(), dependencies{
		start:          func(Config) (childProcess, error) { return child, nil },
		stopFileExists: func(string) (bool, error) { return false, nil },
		generateBreak:  func(uint32) error { return nil },
		stdout:         &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunErrorsKillChild(t *testing.T) {
	for name, setup := range map[string]func(*fakeChild) dependencies{
		"stat": func(child *fakeChild) dependencies {
			return fakeDeps(child, func(string) (bool, error) { return false, errors.New("stat") }, func(uint32) error { return nil })
		},
		"break": func(child *fakeChild) dependencies {
			return fakeDeps(child, func(string) (bool, error) { return true, nil }, func(uint32) error { return errors.New("break") })
		},
		"timeout": func(child *fakeChild) dependencies {
			return fakeDeps(child, func(string) (bool, error) { return true, nil }, func(uint32) error { return nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			child := &fakeChild{pid: 8, wait: make(chan error, 1)}
			config := validConfig()
			config.StopTimeout = 5 * time.Millisecond
			if err := Run(context.Background(), config, setup(child)); err == nil {
				t.Fatal("expected error")
			}
			child.mu.Lock()
			killed := child.killed
			child.mu.Unlock()
			if !killed {
				t.Fatal("child was not killed")
			}
		})
	}
}

func TestRunValidationStartAndContextErrors(t *testing.T) {
	for _, config := range []Config{
		{},
		{Executable: "x"},
		{Executable: "x", StopFile: "s", StopTimeout: 0, PollInterval: time.Millisecond},
		{Executable: "x", StopFile: "s", StopTimeout: time.Second, PollInterval: 0},
	} {
		if err := Run(context.Background(), config, dependencies{}); err == nil {
			t.Errorf("config=%+v", config)
		}
	}
	config := validConfig()
	if err := Run(context.Background(), config, dependencies{start: func(Config) (childProcess, error) { return nil, errors.New("start") }}); err == nil {
		t.Fatal("expected start error")
	}
	child := &fakeChild{pid: 9, wait: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, config, fakeDeps(child, func(string) (bool, error) { return false, nil }, func(uint32) error { return nil })); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestTerminateKillErrorsAndFileExists(t *testing.T) {
	child := &fakeChild{wait: make(chan error), killErr: errors.New("kill")}
	if err := terminateChild(child, child.wait, time.Millisecond, errors.New("cause")); err == nil {
		t.Fatal("expected joined error")
	}
	if exists, err := fileExists(t.TempDir()); err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	if exists, err := fileExists(t.TempDir() + "/missing"); err != nil || exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	if got := formatExit(errors.New("exit 2")); got != "exit 2" {
		t.Fatalf("got=%q", got)
	}
	stubborn := &stubbornChild{wait: make(chan error)}
	if err := terminateChild(stubborn, stubborn.wait, time.Millisecond, errors.New("cause")); err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("err=%v", err)
	}
	originalStat := statFile
	defer func() { statFile = originalStat }()
	statFile = func(string) (os.FileInfo, error) { return nil, errors.New("denied") }
	if exists, err := fileExists("ignored"); err == nil || exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
}

type stubbornChild struct{ wait chan error }

func (child *stubbornChild) PID() int    { return 10 }
func (child *stubbornChild) Wait() error { return <-child.wait }
func (child *stubbornChild) Kill() error { return nil }

func TestWriteExitReportsProcessCode(t *testing.T) {
	var output bytes.Buffer
	cmd := exec.Command("go", "version")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// A synthetic ExitError still carries the process status contract used by
	// os/exec on Windows and non-Windows runners.
	bad := exec.Command("go", "tool", "-bad-flag")
	err := bad.Run()
	if err == nil {
		t.Fatal("expected process error")
	}
	writeExit(&output, err)
	if !strings.Contains(output.String(), "child-exit-code=") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestMainAndCLI(t *testing.T) {
	originalRun, originalExit, originalProcess := runCommandLine, exitProcess, runProcess
	defer func() { runCommandLine, exitProcess, runProcess = originalRun, originalExit, originalProcess }()
	runCommandLine = func([]string) error { return nil }
	main()
	code := 0
	runCommandLine = func([]string) error { return errors.New("cli") }
	exitProcess = func(value int) { code = value }
	main()
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	called := false
	runProcess = func(_ context.Context, config Config, deps dependencies) error {
		called = true
		if config.Executable != "child.exe" || config.WorkDir != "work" || config.StopFile != "stop" || config.StopTimeout != 2*time.Second || config.PollInterval != 3*time.Millisecond || len(config.Args) != 1 || config.Args[0] != "arg" {
			return fmt.Errorf("config=%+v", config)
		}
		if deps.start == nil || deps.stopFileExists == nil || deps.generateBreak == nil || deps.stdout == nil {
			return errors.New("deps")
		}
		return nil
	}
	if err := runCLI([]string{"-exe", "child.exe", "-workdir", "work", "-stop-file", "stop", "-stop-timeout", "2s", "-poll", "3ms", "arg"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("run not called")
	}
	if err := runCLI([]string{"-unknown"}); err == nil {
		t.Fatal("expected parse error")
	}
	if err := runCLI([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err=%v", err)
	}
}

func TestWriteExitPlainError(t *testing.T) {
	var output bytes.Buffer
	writeExit(&output, errors.New("plain"))
	if strings.Contains(output.String(), "child-exit-code") {
		t.Fatalf("output=%q", output.String())
	}
}

func validConfig() Config {
	return Config{Executable: "child.exe", StopFile: "stop", StopTimeout: time.Second, PollInterval: time.Millisecond}
}

func fakeDeps(child *fakeChild, stat func(string) (bool, error), signal func(uint32) error) dependencies {
	return dependencies{start: func(Config) (childProcess, error) { return child, nil }, stopFileExists: stat, generateBreak: signal, stdout: &bytes.Buffer{}}
}
