//go:build windows

package sdk

import (
	"context"

	"github.com/Follen/miniapp-bridge/internal/process"
)

var discoverWindowsProcesses = func(ctx context.Context) ([]process.Process, error) {
	return (process.TasklistFinder{}).FindDetailed(ctx)
}

func (s *Service) Discover(ctx context.Context) ([]Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := discoverWindowsProcesses(ctx)
	if err != nil {
		return nil, &Error{Op: "discover", Component: "process", Err: err}
	}
	out := make([]Target, 0, len(items))
	for _, item := range items {
		out = append(out, Target{PID: item.PID, ParentPID: item.ParentPID, Name: item.Name, Path: item.Path, Version: item.Version})
	}
	return out, nil
}
