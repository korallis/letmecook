package store

import (
	"context"
	"database/sql"

	p "github.com/korallis/letmecook/schemas/execution"
)

// recoverAttempts records uncertainty, never infers process death or resumes work.
// Boot, recovery CAS and events share the startup transaction. No leases exist.
func recoverAttempts(ctx context.Context, tx *sql.Tx, generation string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,task_id,epoch,state,revision FROM attempts WHERE state IN ('assigned','starting','running','result_pending','stopping')`)
	if err != nil {
		return err
	}
	var messages []p.Message
	for rows.Next() {
		m := p.Message{Version: p.FencedVersion, MessageID: newID(), Kind: "transition", Identity: p.Identity{Generation: generation}, To: p.Unknown}
		var revision int64
		if err = rows.Scan(&m.Identity.AttemptID, &m.Identity.TaskID, &m.Identity.Epoch, &m.From, &revision); err != nil {
			rows.Close()
			return err
		}
		m.ExpectedRevision = &revision
		if r := p.CheckTransition(m, m.Identity, m.From, revision); r != p.OK {
			rows.Close()
			return r
		}
		messages = append(messages, m)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, m := range messages {
		if _, err = tx.ExecContext(ctx, "UPDATE attempts SET state='unknown',revision=revision+1 WHERE id=?", m.Identity.AttemptID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE tasks SET state='reconciling' WHERE id=?", m.Identity.TaskID); err != nil {
			return err
		}
		if err = record(ctx, tx, m, *m.ExpectedRevision+1); err != nil {
			return err
		}
	}
	return nil
}
