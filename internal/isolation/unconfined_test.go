//go:build gaffertest

package isolation

import (
	"context"
	"os/exec"
	"testing"
)

// TestOnlyUnconfined is compiled only into this package's explicitly tagged tests.
// It is not available to production binaries, factories or admission policy.
type TestOnlyUnconfined struct{}

func (TestOnlyUnconfined) ID() string            { return "test-only-unconfined" }
func (TestOnlyUnconfined) Kind() string          { return "test-only" }
func (TestOnlyUnconfined) Qualification() string { return QualificationTestOnly }
func (TestOnlyUnconfined) Controls() []string    { return nil }
func (TestOnlyUnconfined) Prepare(context.Context, Workspace) (Launcher, error) {
	return unconfinedLauncher{}, nil
}

type unconfinedLauncher struct{}

func (unconfinedLauncher) Wrap(*exec.Cmd) error { return nil }
func (unconfinedLauncher) Observation() Observation {
	return Observation{Limitations: []string{"test-only; no containment"}}
}
func (unconfinedLauncher) Cleanup() error { return nil }
func TestUnconfinedIsTestOnly(t *testing.T) {
	if (TestOnlyUnconfined{}).Qualification() != QualificationTestOnly {
		t.Fatal("test qualification")
	}
}
