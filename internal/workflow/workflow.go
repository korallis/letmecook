// Package workflow defines owner intent and proposal seams. Planning grants no
// authority; these stubs cannot approve or dispatch an attempt.
package workflow

import (
	"context"
	"errors"
	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/execwire"
	sc "github.com/korallis/letmecook/internal/scheduler"
)

var ErrNotImplemented = errors.New("workflow_not_implemented")

type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type TaskInput struct {
	Version    string      `json:"version"`
	MessageID  string      `json:"message_id"`
	Repository string      `json:"repository"`
	BaseCommit string      `json:"base_commit"`
	Brief      string      `json:"brief"`
	Criteria   []Criterion `json:"criteria"`
	Paths      []string    `json:"paths"`
	Operations []string    `json:"operations"`
}
type Proposal struct {
	Grant    g.Grant           `json:"grant"`
	Decision sc.Decision       `json:"decision"`
	Digests  map[string]string `json:"digests"`
}

// Reader supplies trusted stored inputs through an adapter; workflow never owns
// a database connection or imports the store package.
type Reader interface {
	TaskInput(ctx context.Context, taskID string) (TaskInput, error)
	Eligibility(ctx context.Context, eligibilityID string) (sc.Eligibility, error)
}

// BuildGrant is a side-effect-free proposal seam. S4 supplies trusted persisted inputs.
func BuildGrant(ctx context.Context, source Reader, taskID, eligibilityID string) (Proposal, error) {
	return Proposal{}, ErrNotImplemented
}

// BuildDispatch never admits work; the store remains the admission authority.
func BuildDispatch(ctx context.Context, source Reader, proposal Proposal, messageID string, attemptMS int64) (execwire.DispatchRequest, error) {
	return execwire.DispatchRequest{}, ErrNotImplemented
}
