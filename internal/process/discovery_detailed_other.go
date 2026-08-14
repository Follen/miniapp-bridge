//go:build !windows

package process

import "context"

// FindDetailed preserves the portable discovery contract. Platform-specific
// builds provide process metadata needed by the native attach path.
func (f TasklistFinder) FindDetailed(ctx context.Context) ([]Process, error) {
	return f.Find(ctx)
}
