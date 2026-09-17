package execclient

import (
	"context"
	"encoding/json"
	"github.com/korallis/letmecook/internal/workflow"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
)

func TestTaskInputUsesCanonicalStoreDigest(t *testing.T) {
	brief := store.TaskBrief{Brief: "Update & verify café", Criteria: []store.Criterion{{ID: "c1", Text: "passes"}},
		Paths: []string{"file"}, Operations: []string{"read", "verify", "write"}, Harness: "opencode",
		Settings: json.RawMessage(`{"model":"fixture/model","variant":"high"}`)}
	input := TaskInput(execwire.TaskInput{Brief: brief.Brief, Criteria: []execwire.Criterion{{ID: "c1", Text: "passes"}},
		Paths: brief.Paths, Operations: brief.Operations, Harness: brief.Harness,
		Settings: json.RawMessage(`{ "variant": "high", "model": "fixture/model" }`)})
	got, err := input.Digest()
	if err != nil || got != store.BriefDigest(brief) {
		t.Fatal("client digest diverged", got, store.BriefDigest(brief), err)
	}
	for _, raw := range []string{`null`, `[]`, `{"model":"fixture/model"} {}`} {
		input.Settings = json.RawMessage(raw)
		if _, err = input.Digest(); err == nil {
			t.Fatal("invalid settings accepted", raw)
		}
	}
}

func TestMaximalNormalizedTaskRoundTripsThroughClient(t *testing.T) {
	in := workflow.TaskInput{Version: workflow.Version, MessageID: workflow.IntentID("input-bound", "task"), Repository: "fixture", BaseCommit: strings.Repeat("a", 40), Brief: "x", Criteria: []workflow.Criterion{{ID: "c1", Text: "passes"}}, Paths: []string{"file"}, Operations: []string{"read"}, Harness: "opencode", Settings: json.RawMessage(`{"model":"m"}`)}
	dispatch := workflow.IntentID("input-bound", "dispatch")
	base, err := execwire.Encode(workflow.RunnerInput(in, dispatch))
	if err != nil {
		t.Fatal(err)
	}
	in.Brief = strings.Repeat("x", execwire.MaxBytes-len(base)+1)
	in, err = workflow.Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	wire := workflow.RunnerInput(in, dispatch)
	body, err := execwire.Encode(wire)
	if err != nil || len(body) != execwire.MaxBytes {
		t.Fatal(len(body), err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/v1/input" || r.URL.Query().Get("dispatch_id") != dispatch {
			t.Error("input request binding")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer server.Close()
	client := &Client{http: server.Client(), base: server.URL, attempts: 1, session: workflow.IntentID("input-bound", "session")}
	defer client.Close()
	got, err := client.Input(context.Background(), dispatch)
	if err != nil || !reflect.DeepEqual(execwire.TaskInput(got), wire) {
		t.Fatal("maximal input failed client round trip", err)
	}
}
