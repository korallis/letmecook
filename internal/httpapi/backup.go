package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/jobs"
	p "github.com/korallis/letmecook/schemas/execution"
)

func init() { Register("backup", backupRoutes) }
func backupRoutes(d Deps) []Route {
	return []Route{{Method: "POST", Pattern: "/api/v1/backup", Role: "owner", Class: Control, MaxBody: 4096, Handle: func(ctx context.Context, actor Actor, r Request) (any, *Error) {
		if len(r.Query) != 0 {
			return nil, &Error{400, "invalid_query", ""}
		}
		var body struct {
			Version     string `json:"version"`
			MessageID   string `json:"message_id"`
			Destination string `json:"destination"`
		}
		if closedjson.Decode(r.Body, &body, 4096, nil) != nil || body.Version != "workflow-provisional-v1" || !p.ValidID(body.MessageID) {
			return nil, &Error{400, "malformed", ""}
		}
		if body.Destination == "" || body.Destination == "." || body.Destination == ".." || len(body.Destination) > 128 || strings.ContainsAny(body.Destination, "/\\\x00\r\n") {
			return nil, &Error{400, "invalid_destination", "use a single backup name of at most 128 bytes"}
		}
		if d.Backup == nil {
			return nil, &Error{503, "backup_unavailable", "backup destination root is not configured"}
		}
		// Backups are long work. Without a durable worker, do not acknowledge a
		// non-idempotent synchronous operation under an owner message_id.
		if d.Jobs == nil {
			return nil, &Error{503, "store_unavailable", "jobs_unavailable"}
		}
		// Check the same pause/active-attempt boundary as Create without retaining
		// the pin across other store calls. The worker must recheck when it runs.
		release, err := d.Store.PinArtifacts()
		if err != nil {
			switch err.Error() {
			case "active_execution", "not_paused":
				return nil, &Error{409, err.Error(), ""}
			}
			return nil, &Error{503, "store_unavailable", ""}
		}
		release()
		status, err := d.Store.Status(ctx)
		if err != nil {
			return nil, &Error{503, "store_unavailable", ""}
		}
		job, err := d.Jobs.Submit(ctx, jobs.Job{ID: body.MessageID, Kind: "backup", SubjectID: body.Destination, State: jobs.Queued, DaemonBoot: status.DaemonBoot, CreatedMS: time.Now().UnixMilli()})
		if err != nil {
			return nil, backupFailure(err)
		}
		return Response{Status: http.StatusAccepted, Body: struct {
			JobID string `json:"job_id"`
		}{job.ID}}, nil
	}}}
}
func backupFailure(err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &Error{503, "backup_interrupted", "no completed backup was acknowledged"}
	}
	var refusal *authority.Refusal
	if errors.As(err, &refusal) && refusal.Code == "identity_conflict" {
		return &Error{409, "identity_conflict", ""}
	}
	return &Error{503, "backup_failed", "no completed backup was acknowledged; source data was not removed"}
}
