//go:build windows

package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var powershellJSONDate = regexp.MustCompile(`^/Date\((-?[0-9]+)(?:[+-][0-9]{4})?\)/$`)

var queryWindowsPeerOutput = func(ctx context.Context, pid uint32) ([]byte, error) {
	filter := fmt.Sprintf("ProcessId=%d", pid)
	return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-CimInstance Win32_Process -Filter '"+filter+"' | Select-Object ProcessId,ParentProcessId,CreationDate,CommandLine | ConvertTo-Json -Compress").Output()
}

func queryWindowsPeer(ctx context.Context, pid uint32) (PeerInfo, error) {
	out, err := queryWindowsPeerOutput(ctx, pid)
	if err != nil {
		return PeerInfo{}, err
	}
	var v struct {
		ProcessID uint32 `json:"ProcessId"`
		Parent    uint32 `json:"ParentProcessId"`
		Creation  string `json:"CreationDate"`
		Command   string `json:"CommandLine"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &v); err != nil {
		return PeerInfo{}, err
	}
	if v.ProcessID == 0 {
		return PeerInfo{}, fmt.Errorf("pid %d not found", pid)
	}
	creation := strings.TrimSpace(v.Creation)
	start, err := parsePeerStartTime(creation)
	if err != nil {
		return PeerInfo{}, err
	}
	return PeerInfo{PID: v.ProcessID, ParentPID: v.Parent, StartTime: start, CommandLine: v.Command}, nil
}

func parsePeerStartTime(creation string) (time.Time, error) {
	if creation == "" {
		return time.Time{}, fmt.Errorf("empty process creation time")
	}
	if start, err := time.Parse(time.RFC3339Nano, creation); err == nil {
		return start, nil
	}
	if match := powershellJSONDate.FindStringSubmatch(creation); match != nil {
		milliseconds, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse PowerShell process creation time: %w", err)
		}
		return time.UnixMilli(milliseconds).UTC(), nil
	}
	start, err := time.Parse("20060102150405.000000-070", creation)
	if err != nil && len(creation) == 14 {
		start, err = time.Parse("20060102150405", creation)
	}
	return start, err
}
