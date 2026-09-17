package httpapi

import (
	"context"
	"strings"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/jobs"
)

func init() { Register("backup", backupRoutes) }

type backupCommand struct {
	commandHeader
	Destination string `json:"destination"`
}

func backupRoutes(d Deps) []Route {
	route := ownerRoute("POST", "/api/v1/backup", ownerMutation(d, "backup.create", func(ctx context.Context, actor Actor, r Request, body backupCommand) (any, int, error) {
		if body.Destination == "" || body.Destination == "." || body.Destination == ".." || len(body.Destination) > 128 || strings.ContainsAny(body.Destination, "/\\\x00\r\n") {
			return nil, 0, g.Deny("invalid_destination", "destination")
		}
		if d.Backup == nil {
			return nil, 0, g.Deny("store_unavailable", "backup_unavailable")
		}
		if d.Jobs == nil {
			return nil, 0, g.Deny("store_unavailable", "jobs_unavailable")
		}
		// Only a new intent needs the preflight. A saved owner reply replays even
		// after resume; the worker rechecks pause and active execution at creation.
		release, err := d.Store.PinArtifacts()
		if err != nil {
			switch err.Error() {
			case "active_execution", "not_paused":
				return nil, 0, g.Deny(err.Error(), "")
			default:
				return nil, 0, err
			}
		}
		release()
		job, err := d.Jobs.Submit(ctx, jobs.Job{ID: body.MessageID, Kind: "backup", SubjectID: body.Destination})
		return map[string]string{"job_id": job.ID}, 202, err
	}))
	route.MaxBody = 4096
	return []Route{route}
}
