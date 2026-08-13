//go:build windows

package process

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestQueryWindowsPeerParsingAndErrors(t *testing.T) {
	original := queryWindowsPeerOutput
	t.Cleanup(func() { queryWindowsPeerOutput = original })
	cases := []struct {
		name, output, want string
		err                error
		pid                uint32
	}{
		{name: "command error", err: errors.New("powershell failed"), pid: 7, want: "powershell failed"},
		{name: "invalid json", output: "{", pid: 7, want: "unexpected end"},
		{name: "zero pid", output: `{"ProcessId":0}`, pid: 7, want: "not found"},
		{name: "empty creation", output: `{"ProcessId":7,"ParentProcessId":2}`, pid: 7, want: "empty process creation"},
		{name: "invalid creation", output: `{"ProcessId":7,"CreationDate":"x"}`, pid: 7, want: "cannot parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queryWindowsPeerOutput = func(context.Context, uint32) ([]byte, error) {
				return []byte(tc.output), tc.err
			}
			_, err := queryWindowsPeer(context.Background(), tc.pid)
			if err == nil || !containsError(err, tc.want) {
				t.Fatalf("err=%v, want substring %q", err, tc.want)
			}
		})
	}

	valid := []struct {
		name, creation string
		want           time.Time
	}{
		{name: "rfc3339", creation: "2026-08-12T00:00:00Z"},
		{name: "cim offset", creation: "20260812000000.000000+000"},
		{name: "cim fallback", creation: "20260812000000"},
		{name: "powershell json date", creation: "/Date(1786492800123)/", want: time.UnixMilli(1786492800123).UTC()},
		{name: "powershell json date offset", creation: "/Date(1786492800123+0800)/", want: time.UnixMilli(1786492800123).UTC()},
		{name: "powershell json date before epoch", creation: "/Date(-1)/", want: time.UnixMilli(-1).UTC()},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			queryWindowsPeerOutput = func(context.Context, uint32) ([]byte, error) {
				return []byte(`{"ProcessId":7,"ParentProcessId":2,"CreationDate":"` + tc.creation + `","CommandLine":"--wmpf-appid=app --renderer=webview"}`), nil
			}
			peer, err := queryWindowsPeer(context.Background(), 7)
			if err != nil || peer.PID != 7 || peer.ParentPID != 2 || peer.StartTime.IsZero() {
				t.Fatalf("peer=%+v err=%v", peer, err)
			}
			if !tc.want.IsZero() && !peer.StartTime.Equal(tc.want) {
				t.Fatalf("start=%s want %s", peer.StartTime, tc.want)
			}
		})
	}
}

func TestParsePeerStartTimeRejectsMalformedPowerShellJSONDates(t *testing.T) {
	for _, value := range []string{
		"/Date()/", "/Date(1+080)/", "/Date(1+08000)/", "/Date(1Z)/",
		"/Date(9223372036854775808)/", "20260812000000junk",
	} {
		if _, err := parsePeerStartTime(value); err == nil {
			t.Errorf("parsePeerStartTime(%q) unexpectedly succeeded", value)
		}
	}
}

func TestQueryPeerDefaultCommandCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := queryPeer(ctx, 1)
	if err == nil {
		t.Fatal("expected canceled powershell query error")
	}
}

func containsError(err error, want string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), want)
}
