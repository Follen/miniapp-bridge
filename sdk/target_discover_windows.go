//go:build windows

package sdk

import (
	"context"

	"github.com/Follen/miniapp-bridge/internal/process"
)

func (s *Service) Discover(ctx context.Context) ([]Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := (process.TasklistFinder{}).Find(ctx)
	if err != nil {
		return nil, &Error{Op: "discover", Component: "process", Err: err}
	}
	out := make([]Target, 0, len(items))
	for _, item := range items {
		out = append(out, Target{PID: item.PID, ParentPID: item.ParentPID, Name: item.Name, Path: item.Path, Version: item.Version})
	}
	return out, nil
}
