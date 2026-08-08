package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

var (
	runCommandLine = runCLI
	exitProcess    = os.Exit
	runProcess     = Run
)

func main() {
	if err := runCommandLine(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "runner-error="+err.Error())
		exitProcess(1)
	}
}

func runCLI(args []string) error {
	flags := flag.NewFlagSet("smoke-process-runner", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var config Config
	flags.StringVar(&config.Executable, "exe", "", "child executable path")
	flags.StringVar(&config.WorkDir, "workdir", "", "child working directory")
	flags.StringVar(&config.StopFile, "stop-file", "", "file whose creation requests CTRL_BREAK")
	flags.DurationVar(&config.StopTimeout, "stop-timeout", 15*time.Second, "graceful shutdown timeout")
	flags.DurationVar(&config.PollInterval, "poll", 100*time.Millisecond, "stop-file polling interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config.Args = flags.Args()
	return runProcess(context.Background(), config, systemDependencies())
}
