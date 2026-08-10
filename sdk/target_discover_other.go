//go:build !windows

package sdk

import "context"

func (s *Service) Discover(context.Context) ([]Target, error) {
	return nil, &Error{Op: "discover", Component: "process", Err: ErrNativeUnavailable}
}
