// Package isolation defines launcher qualification. A worktree is not an
// isolation boundary, and the default profile refuses every launch.
package isolation

import (
	"context"
	"errors"
	"os/exec"
)

// Qualification records evidence level, not execution permission.
type Qualification = string

const (
	QualificationUnqualified Qualification = "unqualified"
	QualificationDevelopment Qualification = "development"
	QualificationTestOnly    Qualification = "test-only"
)

var ErrExecutionUnqualified = errors.New("execution_unqualified")

// Workspace separates candidate content from private per-attempt runtime files.
type Workspace struct{ Root, PrivateHome, TempDir, RuntimeDir string }

// Profile prepares a measured launch boundary; qualification is never implied.
type Profile interface {
	ID() string
	Kind() string
	Qualification() string
	Controls() []string
	Prepare(ctx context.Context, w Workspace) (Launcher, error)
}

// Launcher is supplied by the supervisor; adapters must not execute directly.
type Launcher interface {
	Wrap(cmd *exec.Cmd) error
	Observation() Observation
	Cleanup() error
}

// Observation records measured controls and explicit limitations.
type Observation struct {
	ProfileDigest, RuntimeDigest, OS, Arch string
	Controls, Limitations                  []string
}

// Unqualified is the refusing default; no platform detection upgrades it.
type Unqualified struct{}

func (Unqualified) ID() string            { return "unqualified" }
func (Unqualified) Kind() string          { return "unqualified" }
func (Unqualified) Qualification() string { return QualificationUnqualified }
func (Unqualified) Controls() []string    { return nil }
func (Unqualified) Prepare(context.Context, Workspace) (Launcher, error) {
	return nil, ErrExecutionUnqualified
}
