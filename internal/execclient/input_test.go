package execclient

import (
	"encoding/json"
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
