package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type Config struct {
	Executable   string
	WorkDir      string
	StopFile     string
	Args         []string
	StopTimeout  time.Duration
	PollInterval time.Duration
}

type childProcess interface {
	PID() int
	Wait() error
	Kill() error
}

type dependencies struct {
	start          func(Config) (childProcess, error)
	stopFileExists func(string) (bool, error)
	generateBreak  func(uint32) error
	stdout         io.Writer
}

var statFile = os.Stat

func Run(ctx context.Context, config Config, deps dependencies) error {
	if config.Executable == "" {
		return errors.New("-exe is required")
	}
	if config.StopFile == "" {
		return errors.New("-stop-file is required")
	}
	if config.StopTimeout <= 0 {
		return errors.New("-stop-timeout must be positive")
	}
	if config.PollInterval <= 0 {
		return errors.New("-poll must be positive")
	}
	child, err := deps.start(config)
	if err != nil {
		return fmt.Errorf("start child: %w", err)
	}
	fmt.Fprintf(deps.stdout, "child-pid=%d\n", child.PID())

	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	poll := time.NewTicker(config.PollInterval)
	defer poll.Stop()

	for {
		select {
		case waitErr := <-waited:
			writeExit(deps.stdout, waitErr)
			return waitErr
		case <-ctx.Done():
			return terminateChild(child, waited, config.StopTimeout, ctx.Err())
		case <-poll.C:
			exists, statErr := deps.stopFileExists(config.StopFile)
			if statErr != nil {
				return terminateChild(child, waited, config.StopTimeout, fmt.Errorf("check stop file: %w", statErr))
			}
			if !exists {
				continue
			}
			fmt.Fprintln(deps.stdout, "stop-requested=true")
			if err := deps.generateBreak(uint32(child.PID())); err != nil {
				return terminateChild(child, waited, config.StopTimeout, fmt.Errorf("send CTRL_BREAK: %w", err))
			}
			return waitForGracefulExit(child, waited, config.StopTimeout, deps.stdout)
		}
	}
}

func waitForGracefulExit(child childProcess, waited <-chan error, timeout time.Duration, output io.Writer) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		writeExit(output, err)
		return err
	case <-timer.C:
		return terminateChild(child, waited, timeout, errors.New("graceful shutdown timed out"))
	}
}

func terminateChild(child childProcess, waited <-chan error, timeout time.Duration, cause error) error {
	killErr := child.Kill()
	if killErr != nil {
		return errors.Join(cause, fmt.Errorf("kill child: %w", killErr))
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waited:
		return cause
	case <-timer.C:
		return errors.Join(cause, errors.New("child did not exit after kill"))
	}
}

func formatExit(err error) string {
	if err == nil {
		return "0"
	}
	return err.Error()
}

func writeExit(output io.Writer, err error) {
	fmt.Fprintf(output, "child-exit-status=%s\n", formatExit(err))
	if err == nil {
		fmt.Fprintln(output, "child-exit-code=0")
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		fmt.Fprintf(output, "child-exit-code=%d\n", exitErr.ProcessState.ExitCode())
	}
}

func fileExists(path string) (bool, error) {
	_, err := statFile(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
