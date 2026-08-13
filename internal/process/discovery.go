package process

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Process struct {
	PID       uint32
	ParentPID uint32
	Name      string
	Path      string
	Version   int
	Identity  TargetIdentity
}

// TargetIdentity binds a discovered process to the exact instance being used.
type TargetIdentity struct {
	PID          uint32
	StartTime    time.Time
	AppID        string
	RendererType string
	DiscoveredAt time.Time
}
type Finder interface {
	Find(context.Context) ([]Process, error)
}
type TasklistFinder struct{ Names []string }

var tasklistOutput = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH").Output()
}

func (f TasklistFinder) Find(ctx context.Context) ([]Process, error) {
	out, err := tasklistOutput(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range f.Names {
		want[strings.ToLower(n)] = true
	}
	var result []Process
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.Trim(line, "\"\r"), "\",\"")
		if len(parts) < 2 {
			continue
		}
		if len(want) > 0 && !want[strings.ToLower(parts[0])] {
			continue
		}
		pid, e := strconv.ParseUint(parts[1], 10, 32)
		if e == nil {
			result = append(result, Process{PID: uint32(pid), Name: parts[0], Version: ParseVersion(parts[0])})
		}
	}
	return result, nil
}

var versionRE = regexp.MustCompile(`\d+`)

func ParseVersion(s string) int {
	matches := versionRE.FindAllString(s, -1)
	if len(matches) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(matches[len(matches)-1])
	return n
}

// SelectParent chooses the parent PID occurring most often among child processes,
// matching the reference debugger's deterministic attach heuristic.
func SelectParent(ps []Process, childName string) (uint32, error) {
	counts := map[uint32]int{}
	var order []uint32
	seen := map[uint32]bool{}
	for _, p := range ps {
		if p.Name == childName && p.ParentPID != 0 {
			counts[p.ParentPID]++
			if !seen[p.ParentPID] {
				seen[p.ParentPID] = true
				order = append(order, p.ParentPID)
			}
		}
	}
	var selected uint32
	max := 0
	for _, pid := range order {
		if n := counts[pid]; n >= max {
			selected, max = pid, n
		}
	}
	if selected == 0 {
		return 0, errors.New("parent process not found")
	}
	return selected, nil
}
func SelectTarget(ps []Process, names ...string) (Process, error) {
	for _, p := range ps {
		for _, n := range names {
			if strings.EqualFold(p.Name, n) {
				return p, nil
			}
		}
	}
	return Process{}, errors.New("target process not found")
}
