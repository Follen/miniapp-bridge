package process

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PeerInfo struct {
	PID         uint32
	ParentPID   uint32
	StartTime   time.Time
	CommandLine string
}

// peerQuery is replaceable in tests and keeps platform-specific process APIs out of discovery.
var peerQuery = queryPeer

func BindTarget(ctx context.Context, p Process, expectedAppID, expectedRenderer string, now time.Time) (Process, error) {
	if p.PID == 0 {
		return Process{}, errors.New("target identity: zero pid")
	}
	peer, err := peerQuery(ctx, p.PID)
	if err != nil {
		return Process{}, fmt.Errorf("target identity: peer query: %w", err)
	}
	if peer.PID != p.PID {
		return Process{}, fmt.Errorf("target identity: pid mismatch: got %d want %d", peer.PID, p.PID)
	}
	if peer.StartTime.IsZero() {
		return Process{}, errors.New("target identity: missing start time")
	}
	appID, renderer := ParseCommandLineIdentity(peer.CommandLine)
	if expectedRenderer == "host" {
		if renderer != "" {
			return Process{}, fmt.Errorf("target identity: renderer host mismatch: got %q", renderer)
		}
		p.ParentPID, p.Identity = peer.ParentPID, TargetIdentity{
			PID: peer.PID, StartTime: peer.StartTime, RendererType: "host", DiscoveredAt: now,
		}
		return p, nil
	}
	if appID == "" {
		return Process{}, errors.New("target identity: missing app id")
	}
	if renderer == "" {
		return Process{}, errors.New("target identity: missing renderer type")
	}
	if strings.HasPrefix(strings.ToLower(appID), "preload-") {
		return Process{}, errors.New("target identity: preload process rejected")
	}
	if expectedAppID != "" && appID != expectedAppID {
		return Process{}, fmt.Errorf("target identity: app id mismatch: got %q want %q", appID, expectedAppID)
	}
	if expectedRenderer != "" && renderer != expectedRenderer {
		return Process{}, fmt.Errorf("target identity: renderer mismatch: got %q want %q", renderer, expectedRenderer)
	}
	p.ParentPID, p.Identity = peer.ParentPID, TargetIdentity{PID: peer.PID, StartTime: peer.StartTime, AppID: appID, RendererType: renderer, DiscoveredAt: now}
	return p, nil
}

func ParseCommandLineIdentity(commandLine string) (appID, renderer string) {
	for _, field := range strings.Fields(commandLine) {
		if strings.HasPrefix(field, "--wmpf-appid=") {
			appID = strings.TrimPrefix(field, "--wmpf-appid=")
		}
		if strings.HasPrefix(field, "--renderer=") {
			renderer = strings.TrimPrefix(field, "--renderer=")
		}
		if renderer == "" && strings.Contains(strings.ToLower(field), "renderer") {
			renderer = "renderer"
		}
	}
	return appID, renderer
}

func queryPeer(ctx context.Context, pid uint32) (PeerInfo, error) { return queryWindowsPeer(ctx, pid) }
