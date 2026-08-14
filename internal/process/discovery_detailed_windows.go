//go:build windows

package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

var queryWindowsProcessesOutput = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,Name,ParentProcessId,ExecutablePath,CommandLine | ConvertTo-Json -Compress").Output()
}

type windowsProcessDetail struct {
	PID       uint32 `json:"ProcessId"`
	Name      string `json:"Name"`
	ParentPID uint32 `json:"ParentProcessId"`
	Path      string `json:"ExecutablePath"`
	Command   string `json:"CommandLine"`
}

// FindDetailed returns the same candidates as tasklist, enriched from one
// CIM snapshot so callers can immediately choose a versioned attach target.
func (f TasklistFinder) FindDetailed(ctx context.Context) ([]Process, error) {
	out, err := queryWindowsProcessesOutput(ctx)
	if err != nil {
		return nil, err
	}
	details, err := decodeWindowsProcessDetails(out)
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(f.Names))
	for _, name := range f.Names {
		want[strings.ToLower(name)] = true
	}
	result := make([]Process, 0, len(details))
	for _, detail := range details {
		if detail.PID == 0 || (len(want) > 0 && !want[strings.ToLower(detail.Name)]) {
			continue
		}
		version := ParseVersion(detail.Path)
		if version == 0 {
			version = ParseVersion(detail.Name)
		}
		result = append(result, Process{
			PID: detail.PID, ParentPID: detail.ParentPID, Name: detail.Name,
			Path: detail.Path, Version: version,
		})
	}
	return result, nil
}

func decodeWindowsProcessDetails(data []byte) ([]windowsProcessDetail, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	if data[0] == '{' {
		var one windowsProcessDetail
		if err := json.Unmarshal(data, &one); err != nil {
			return nil, err
		}
		return []windowsProcessDetail{one}, nil
	}
	var many []windowsProcessDetail
	if err := json.Unmarshal(data, &many); err != nil {
		return nil, fmt.Errorf("decode Windows process details: %w", err)
	}
	return many, nil
}
