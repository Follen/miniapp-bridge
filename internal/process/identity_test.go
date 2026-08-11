package process

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBindTargetIdentity(t *testing.T) {
	old := peerQuery
	defer func() { peerQuery = old }()
	now := time.Unix(100, 0)
	start := time.Unix(50, 0)
	cases := []struct {
		name, cmd, app, renderer string
		pid, peer                uint32
		start                    time.Time
		wantErr                  string
	}{
		{"ok", "--wmpf-appid=app-1 --renderer=webview", "app-1", "webview", 7, 7, start, ""},
		{"preload", "--wmpf-appid=preload-1 --renderer=webview", "", "", 7, 7, start, "preload"},
		{"pid reuse", "--wmpf-appid=app-1 --renderer=webview", "app-1", "", 7, 8, start, "pid mismatch"},
		{"missing start", "--wmpf-appid=app-1 --renderer=webview", "app-1", "", 7, 7, time.Time{}, "missing start"},
		{"missing app", "--renderer=webview", "", "", 7, 7, start, "missing app id"},
		{"missing renderer", "--wmpf-appid=app-1", "", "", 7, 7, start, "missing renderer type"},
		{"app mismatch", "--wmpf-appid=app-1 --renderer=webview", "other", "", 7, 7, start, "app id mismatch"},
		{"renderer mismatch", "--wmpf-appid=app-1 --renderer=webview", "app-1", "native", 7, 7, start, "renderer mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peerQuery = func(context.Context, uint32) (PeerInfo, error) {
				return PeerInfo{PID: tc.peer, StartTime: tc.start, CommandLine: tc.cmd}, nil
			}
			_, err := BindTarget(context.Background(), Process{PID: tc.pid}, tc.app, tc.renderer, now)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestBindTargetQueryErrorAndZeroPID(t *testing.T) {
	old := peerQuery
	defer func() { peerQuery = old }()
	peerQuery = func(context.Context, uint32) (PeerInfo, error) { return PeerInfo{}, context.Canceled }
	if _, err := BindTarget(context.Background(), Process{PID: 1}, "", "", time.Now()); err == nil {
		t.Fatal("expected query error")
	}
	if _, err := BindTarget(context.Background(), Process{}, "", "", time.Now()); err == nil {
		t.Fatal("expected zero pid error")
	}
}

func TestParseCommandLineIdentity(t *testing.T) {
	a, r := ParseCommandLineIdentity(`foo --wmpf-appid=abc --renderer=webview`)
	if a != "abc" || r != "webview" {
		t.Fatalf("%q %q", a, r)
	}
	a, r = ParseCommandLineIdentity("renderer-process")
	if r != "renderer" {
		t.Fatal(r)
	}
}
