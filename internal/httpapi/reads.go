package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	g "github.com/korallis/letmecook/internal/authority"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/runstream"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
)

func init() { Register("reads", ownerReads) }
func readRoute(path string, allowed []string, fn func(context.Context, Actor, Request) (any, error)) Route {
	return ownerRoute("GET", path, func(ctx context.Context, actor Actor, r Request) (any, *Error) {
		if err := strictQuery(r, allowed...); err != nil {
			return nil, workflowError(err)
		}
		out, err := fn(ctx, actor, r)
		return out, workflowError(err)
	})
}
func pathID(r Request) error {
	if !p.ValidID(r.Path["id"]) {
		return g.Deny("invalid_id", "id")
	}
	return nil
}
func ownerReads(d Deps) []Route {
	routes := []Route{
		readRoute("/api/v1/repositories/{id}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			if !g.ValidActor(r.Path["id"]) {
				return nil, g.Deny("invalid_id", "repository")
			}
			out, err := d.Store.RepositoryProfile(ctx, a.Fingerprint, r.Path["id"])
			if errors.Is(err, i.Conflict) {
				err = g.Deny("not_found", "repository")
			}
			return out, err
		}),
		readRoute("/api/v1/eligibility/{id}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			if !g.ValidActor(r.Path["id"]) {
				return nil, g.Deny("invalid_id", "eligibility")
			}
			return d.Store.Eligibility(ctx, r.Path["id"])
		}),
		readRoute("/api/v1/runners", nil, func(ctx context.Context, a Actor, r Request) (any, error) { return d.Store.Runners(ctx) }),
		readRoute("/api/v1/tasks", []string{"after", "limit"}, func(ctx context.Context, a Actor, r Request) (any, error) {
			limit, err := queryBound(r, "limit", 32, aMaxItems())
			if err != nil {
				return nil, err
			}
			tasks, err := d.Store.Tasks(ctx, r.Query.Get("after"), int(limit))
			next := r.Query.Get("after")
			if len(tasks) > 0 {
				next = tasks[len(tasks)-1].Brief.TaskID
			}
			return struct {
				Tasks       []store.Task `json:"tasks"`
				NextAfter   string       `json:"next_after"`
				PollAfterMS int          `json:"poll_after_ms"`
			}{tasks, next, 500}, err
		}),
		readRoute("/api/v1/tasks/{id}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			if err := pathID(r); err != nil {
				return nil, err
			}
			return d.Store.Task(ctx, r.Path["id"])
		}),
		readRoute("/api/v1/tasks/{id}/proposal", []string{"eligibility"}, func(ctx context.Context, a Actor, r Request) (any, error) {
			if err := pathID(r); err != nil {
				return nil, err
			}
			if r.Query.Get("eligibility") == "" {
				return nil, g.Deny("invalid_query", "eligibility")
			}
			return buildProposal(ctx, d, r.Path["id"], r.Query.Get("eligibility"))
		}),
		readRoute("/api/v1/jobs/{id}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			if err := pathID(r); err != nil {
				return nil, err
			}
			if d.Jobs == nil {
				return nil, g.Deny("store_unavailable", "jobs")
			}
			return d.Jobs.Get(ctx, r.Path["id"])
		}),
		readRoute("/api/v1/attempts/{id}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			if err := pathID(r); err != nil {
				return nil, err
			}
			return d.Store.AttemptView(ctx, r.Path["id"])
		}),
		readRoute("/api/v1/attempts/{id}/stream", []string{"after", "limit"}, func(ctx context.Context, a Actor, r Request) (any, error) {
			if err := pathID(r); err != nil {
				return nil, err
			}
			if _, err := d.Store.AttemptView(ctx, r.Path["id"]); err != nil {
				return nil, err
			}
			after, err := queryBound(r, "after", 0, p.MaxInteger)
			if err != nil {
				return nil, err
			}
			limit, err := queryBound(r, "limit", 32, 128)
			if err != nil || limit == 0 {
				return nil, g.Deny("invalid_query", "limit")
			}
			if d.Sinks == nil {
				return nil, g.Deny("store_unavailable", "streams")
			}
			records, ack, err := d.Sinks.Window(r.Path["id"], after, limit)
			if err != nil {
				return nil, err
			}
			return boundedStreamWindow(records, ack)
		}),
		readRoute("/api/v1/attempts/{id}/artifacts", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			return d.Store.AttemptArtifacts(ctx, r.Path["id"])
		}),
		readRoute("/api/v1/attempts/{id}/usage", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			return d.Store.AttemptUsageRows(ctx, r.Path["id"])
		}),
		readRoute("/api/v1/stops/{id}", []string{"attempt_id"}, func(ctx context.Context, a Actor, r Request) (any, error) {
			if err := pathID(r); err != nil {
				return nil, err
			}
			if !p.ValidID(r.Query.Get("attempt_id")) {
				return nil, g.Deny("invalid_query", "attempt_id")
			}
			return d.Store.StopStatus(ctx, r.Path["id"], r.Query.Get("attempt_id"))
		}),
		readRoute("/api/v1/events", []string{"after", "limit", "task_id"}, func(ctx context.Context, a Actor, r Request) (any, error) {
			after, err := queryBound(r, "after", 0, p.MaxInteger)
			if err != nil {
				return nil, err
			}
			limit, err := queryBound(r, "limit", 32, aMaxItems())
			if err != nil {
				return nil, err
			}
			return d.Store.Events(ctx, after, int(limit), r.Query.Get("task_id"))
		}),
		readRoute("/api/v1/daemon", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
			state, err := d.Store.Paused(ctx)
			if err != nil {
				return nil, err
			}
			meta, err := d.Store.Status(ctx)
			if err != nil {
				return nil, err
			}
			history, err := d.Store.WorkflowRestoreHistory(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"paused": state.Paused, "reason": state.Reason, "generation": meta.Generation, "daemon_boot": meta.DaemonBoot, "schema": meta.SchemaVersion, "restore_history": history, "policy": d.Policy}, nil
		}),
	}
	blob := readRoute("/api/v1/artifacts/blobs/{sha256}", nil, func(ctx context.Context, a Actor, r Request) (any, error) {
		reader, size, err := d.Store.OpenWorkflowBlob(ctx, r.Path["sha256"])
		if err != nil {
			return nil, err
		}
		return Response{Status: 200, Body: reader, Header: http.Header{"Content-Type": []string{"application/octet-stream"}, "Content-Length": []string{strconv.FormatInt(size, 10)}}}, nil
	})
	blob.Class = Bulk
	routes = append(routes, blob)
	return routes
}
func aMaxItems() int64 { return int64(a.MaxItems) }

// Stream pages are bounded by both the requested record count and the read API's
// encoded response limit. Ack remains the sink's global durable watermark; the
// next cursor is the last returned record, not Ack.Through.
type streamWindow struct {
	Records     []runstream.Record `json:"records"`
	Ack         runstream.Ack      `json:"ack"`
	PollAfterMS int                `json:"poll_after_ms"`
}

func boundedStreamWindow(records []runstream.Record, ack runstream.Ack) (streamWindow, error) {
	out := streamWindow{Records: []runstream.Record{}, Ack: ack, PollAfterMS: 500}
	base, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	size := len(base)
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			return out, err
		}
		next := size + len(encoded)
		if len(out.Records) > 0 {
			next++
		} // array comma
		if next > a.MaxBytes {
			// A valid bounded runstream record fits alone. Refuse a corrupt
			// oversized record rather than returning an unadvanceable page.
			if len(out.Records) == 0 {
				return out, g.Deny("store_unavailable", "streams")
			}
			break
		}
		out.Records = append(out.Records, record)
		size = next
	}
	return out, nil
}
