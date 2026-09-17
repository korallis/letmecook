package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	c "github.com/korallis/letmecook/internal/control"
	i "github.com/korallis/letmecook/internal/identity"
)

func TestOwnerResumeScopeReplayAndAtomicity(t *testing.T) {
	f, d, _ := controlFixture(t)
	task := d.Assignment.Identity.TaskID
	other := newID()
	if _, err := f.s.db.Exec("INSERT INTO tasks VALUES(?,'ready')", other); err != nil {
		t.Fatal(err)
	}
	stop := func(kind c.Kind, taskID string) string {
		t.Helper()
		request := c.Request{ID: newID(), Kind: kind, TaskID: taskID, Cause: "operator"}
		if kind == c.CancelAttempt {
			request.AttemptID = d.Assignment.Identity.AttemptID
		}
		if _, err := f.s.RequestStop(ctx, f.owner, request); err != nil {
			t.Fatal(err)
		}
		return request.ID
	}
	pause, otherPause, global, cancel := stop(c.PauseTask, task), stop(c.PauseTask, other), stop(c.GlobalStop, ""), stop(c.CancelAttempt, task)
	if err := f.s.StopDispatch(ctx, f.owner, task); err != nil {
		t.Fatal(err)
	}
	legacy := dispatchID(task, "legacy-stop")
	var before int
	if err := f.s.db.QueryRow("SELECT count(*) FROM control_stops").Scan(&before); err != nil {
		t.Fatal(err)
	}
	key := newID()
	if _, err := f.s.ResumeLatches(ctx, f.runner, task, key); !errors.Is(err, i.Denied) {
		t.Fatal("runner resume", err)
	}
	ids, err := f.s.ResumeLatches(ctx, f.owner, task, key)
	want := []string{pause, legacy}
	slices.Sort(want)
	if err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatal(ids, err, want)
	}
	later := stop(c.PauseTask, task)
	replay, err := f.s.ResumeLatches(ctx, f.owner, task, key)
	if err != nil || !reflect.DeepEqual(replay, ids) {
		t.Fatal("replay", replay, err)
	}
	if _, err = f.s.ResumeLatches(ctx, f.owner, other, key); err == nil {
		t.Fatal("conflicting resume accepted")
	}
	marker := func(id string, cleared bool) {
		t.Helper()
		var raw string
		err := f.s.db.QueryRow("SELECT body FROM reconcile_reports WHERE id=?", "latch-cleared:"+id).Scan(&raw)
		if !cleared {
			if err == nil {
				t.Fatal("unexpected clearance", id, raw)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		var record ownerLatchClearance
		if json.Unmarshal([]byte(raw), &record) != nil || record.StopID != id || record.DaemonBoot != f.s.meta.DaemonBoot || record.ClearedMS <= 0 || record.Terminal != "" || record.AttemptID != "" || record.Cause != "operator" || record.Actor != f.owner {
			t.Fatal(raw)
		}
		var fields map[string]any
		if json.Unmarshal([]byte(raw), &fields) != nil || len(fields) != 9 {
			t.Fatal("S5 clearance wire shape", raw)
		}
	}
	marker(pause, true)
	marker(legacy, true)
	for _, id := range []string{otherPause, global, cancel, later} {
		marker(id, false)
	}
	if _, err = f.s.SetPaused(ctx, f.owner, newID(), true, "maintenance", false); err != nil {
		t.Fatal(err)
	}
	// If unpause fails, every clearance must roll back in the same transaction.
	sqlExec(t, f.s, `CREATE TRIGGER fail_resume BEFORE UPDATE ON daemon_state BEGIN SELECT RAISE(ABORT,'test resume failure'); END;`)
	resumeKey := newID()
	if _, err = f.s.SetPaused(ctx, f.owner, resumeKey, false, "", false); err == nil {
		t.Fatal("failed unpause acknowledged")
	}
	marker(global, false)
	if state, err := f.s.Paused(ctx); err != nil || !state.Paused {
		t.Fatal(state, err)
	}
	sqlExec(t, f.s, "DROP TRIGGER fail_resume")
	state, err := f.s.SetPaused(ctx, f.owner, resumeKey, false, "", false)
	if err != nil || state.Paused || !reflect.DeepEqual(state.ClearedLatches, []string{global}) {
		t.Fatal(state, err)
	}
	newGlobal := stop(c.GlobalStop, "")
	replayState, err := f.s.SetPaused(ctx, f.owner, resumeKey, false, "", false)
	if err != nil || !reflect.DeepEqual(replayState, state) {
		t.Fatal(replayState, err)
	}
	marker(global, true)
	marker(newGlobal, false)
	marker(cancel, false)
	marker(otherPause, false)
	marker(later, false)
	rowCount(t, f.s, "control_stops", before+2)
	rowCount(t, f.s, "dispatch_releases", 0)
	// Resume never resolves this still-live attempt, its cancel_attempt latch,
	// or stops created after the retained resume command.
	next := f.request
	next.ID = newID()
	_, err = f.s.Dispatch(ctx, next)
	requireReason(t, err, "stop_latched")
	if latched, err := f.s.TaskLatched(ctx, task); err != nil || !latched {
		t.Fatal("resume cleared unresolved cancellation or later stops", latched, err)
	}
}

func TestOwnerResumeAllowsFreshAdmission(t *testing.T) {
	for _, kind := range []c.Kind{c.GlobalStop, c.PauseTask} {
		t.Run(string(kind), func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			// This admission fixture predates CreateTask and otherwise inserts the
			// task only at dispatch. Resume requires an existing owner task.
			if _, err := f.s.db.Exec("INSERT INTO tasks VALUES(?,'ready')", f.grant.TaskID); err != nil {
				t.Fatal(err)
			}
			stop := c.Request{ID: newID(), Kind: kind, Cause: "operator"}
			if kind == c.PauseTask {
				stop.TaskID = f.grant.TaskID
				if err := f.s.StopDispatch(ctx, f.owner, stop.TaskID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.s.RequestStop(ctx, f.owner, stop); err != nil {
				t.Fatal(err)
			}
			_, err := f.s.Dispatch(ctx, f.request)
			want := "stop_latched"
			if kind == c.PauseTask {
				want = "stopped"
			}
			requireReason(t, err, want)
			if kind == c.GlobalStop {
				state, err := f.s.SetPaused(ctx, f.owner, newID(), false, "owner resume", false)
				if err != nil || len(state.ClearedLatches) != 1 || state.ClearedLatches[0] != stop.ID {
					t.Fatal(state, err)
				}
			} else {
				cleared, err := f.s.ResumeLatches(ctx, f.owner, stop.TaskID, newID())
				if err != nil || len(cleared) != 2 {
					t.Fatal(cleared, err)
				}
			}
			if latched, err := f.s.TaskLatched(ctx, f.grant.TaskID); err != nil || latched {
				t.Fatal("reconcile and admission disagree", latched, err)
			}
			d := admitted(t, f)
			sess := sessionFor(t, f)
			if err = f.s.AcknowledgeAssignment(ctx, f.runner, d.ID, acceptFor(f, d)); err != nil {
				t.Fatal(err)
			}
			x := executionFixture{dispatchFixture: f, d: d, session: sess}
			x.run(t)
			if kind == c.PauseTask {
				rowCount(t, f.s, "dispatch_stops", 1)
				rowCount(t, f.s, "control_stops", 2)
			} else {
				rowCount(t, f.s, "control_stops", 1)
			}
		})
	}
}
