// Package inference defines the per-attempt model boundary. The supervisor owns
// gateway credentials; a worker receives only a scoped token. No account routing.
package inference

import (
	"context"
	p "github.com/korallis/letmecook/schemas/execution"
	"time"
)

// CredentialRef names supervisor-only material; it never contains a credential.
type CredentialRef struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}
type Gateway struct {
	ID, BaseURL       string
	CABundle          string
	CredentialRef     CredentialRef
	Protocols, Models []string
	ProfileDigest     string
}
type Scope struct {
	Identity          p.Identity
	Token             string
	Models, Protocols []string
	Gateway           Gateway
	Limits            Limits
}
type Limits struct {
	Requests, RequestBytes, ResponseBytes int64
	RequestTimeout                        time.Duration
}
type Receipt struct {
	RequestID        string    `json:"request_id"`
	Protocol         string    `json:"protocol"`
	Model            string    `json:"model"`
	Status           int       `json:"status"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	BytesIn          int64     `json:"bytes_in"`
	BytesOut         int64     `json:"bytes_out"`
	Started          time.Time `json:"started"`
	Ended            time.Time `json:"ended"`
	Terminal         bool      `json:"terminal"`
	Source           string    `json:"source"`
}
type ReceiptSink interface {
	Reserve(ctx context.Context, r Receipt) error
	Complete(ctx context.Context, r Receipt) error
}
type State struct {
	Reservations     int64 `json:"reservations"`
	TerminalReceipts int64 `json:"terminal_receipts"`
	InFlight         int64 `json:"in_flight"`
	Quiescent        bool  `json:"quiescent"`
}
type Boundary interface {
	Addr() string
	State() State
	Close(ctx context.Context) State
}
