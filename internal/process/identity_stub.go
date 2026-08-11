//go:build !windows

package process

import (
	"context"
	"errors"
)

func queryWindowsPeer(context.Context, uint32) (PeerInfo, error) {
	return PeerInfo{}, errors.New("windows peer identity unavailable on this platform")
}
