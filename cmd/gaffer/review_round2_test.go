package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPSDevelopmentIdentityCannotBypassConsent(t *testing.T) {
	for _, qualification := range []string{"", "unqualified", "development", "test-only"} {
		for _, supported := range []bool{false, true} {
			if qualification == "development" && !supported {
				continue
			}
			t.Run(qualification+map[bool]string{true: "-supported", false: "-unsupported"}[supported], func(t *testing.T) {
				f := newOwnerFixture(t)
				endpoint, client := f.serve(t)
				create := taskBody(f, "mislabel-task")
				postWorkflow(t, client, endpoint, "/api/v1/tasks", create, 201)
				facts := f.facts
				facts.Revision = 2
				facts.Isolation.Qualification, facts.Isolation.Supported = qualification, supported
				body := commandBody("mislabel-publish")
				body["expected_revision"], body["facts"] = 1, facts
				raw, _ := postWorkflow(t, client, endpoint, "/api/v1/eligibility", body, 422)
				if !strings.Contains(string(raw), "development_isolation_refused") {
					t.Fatal(string(raw))
				}
				// Model a historical row admitted by the old validator. Do not weaken the
				// immutable-row trigger or claim this is a supported write path.
				db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				encoded, _ := json.Marshal(facts)
				if _, err = db.Exec(`INSERT INTO dispatch_eligibility SELECT id,2,?,actor,repository_id,runner_id,root FROM dispatch_eligibility WHERE id=? AND revision=1`, string(encoded), facts.ID); err != nil {
					t.Fatal(err)
				}
				approval := commandBody("mislabel-approve")
				approval["eligibility_id"], approval["proposal_digest"], approval["allow_development_isolation"] = facts.ID, strings.Repeat("a", 64), false
				raw, _ = postWorkflow(t, client, endpoint, "/api/v1/tasks/"+create["message_id"].(string)+"/approve", approval, 400)
				if !strings.Contains(string(raw), "isolation_qualification") {
					t.Fatal(string(raw))
				}
			})
		}
	}
}

func TestHTTPSTaskResumeReplaysDurableClearances(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t)
	create := taskBody(f, "resume-task")
	postWorkflow(t, client, endpoint, "/api/v1/tasks", create, 201)
	task := create["message_id"].(string)
	postWorkflow(t, client, endpoint, "/api/v1/tasks/"+task+"/stop", commandBody("resume-stop"), 202)
	body := commandBody("resume-command")
	first := mutation(t, client, endpoint, "/api/v1/tasks/"+task+"/resume", body, 200)
	var response struct {
		Cleared []string `json:"cleared_latches"`
	}
	if decodeReply(first, &response) != nil || len(response.Cleared) != 2 {
		t.Fatal(string(first))
	}
	// Both the ordinary task stop and legacy StopDispatch marker are retained.
	db, err := sql.Open("sqlite", filepath.Join(f.state, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range response.Cleared {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM reconcile_reports WHERE id=?", "latch-cleared:"+id).Scan(&n); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	// TODO(integration): fresh dispatch after resume depends on the integrator's
	// suppression changes; this test proves durable marker writes, not admission.
}
