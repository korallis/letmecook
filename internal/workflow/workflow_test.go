package workflow

import (
	"encoding/json"
	"errors"
	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/execwire"
	"strings"
	"testing"
)

func TestTaskTotalEnvelopeBoundary(t *testing.T) {
	input := TaskInput{Version: Version, MessageID: IntentID("size-test", "task"), Repository: "fixture", BaseCommit: strings.Repeat("a", 40), Criteria: []Criterion{{ID: "c1", Text: "passes"}}, Paths: []string{"file"}, Operations: []string{"read"}, Harness: "fake", Settings: json.RawMessage(`{"attempts":[{"mode":"noop","edits":[]}]}`)}
	raw, _ := json.Marshal(input)
	input.Brief = strings.Repeat("x", execwire.MaxBytes-len(raw))
	var refusal *g.Refusal
	if _, err := Normalize(input); !errors.As(err, &refusal) || refusal.Code != "oversized" {
		t.Fatal("old owner bound not refused as oversized", err)
	}
	wire, err := json.Marshal(RunnerInput(input, input.MessageID))
	if err != nil {
		t.Fatal(err)
	}
	overhead := len(wire) - execwire.MaxBytes
	if overhead != 141 {
		t.Fatalf("runner wrapper overhead changed: %d", overhead)
	}
	input.Brief = input.Brief[:len(input.Brief)-overhead]
	normalized, err := Normalize(input)
	if err != nil {
		t.Fatal("maximal wire envelope refused", err)
	}
	encoded, err := execwire.Encode(RunnerInput(normalized, input.MessageID))
	if err != nil || len(encoded) != execwire.MaxBytes {
		t.Fatal(len(encoded), err)
	}
	input.Brief += "x"
	if _, err := Normalize(input); !errors.As(err, &refusal) || refusal.Code != "oversized" {
		t.Fatal("one-byte overflow not refused", err)
	}
}

func TestSettingsCanonicalExpansionBound(t *testing.T) {
	content := strings.Repeat("<", 3500) + strings.Repeat("a", 30000)
	raw := json.RawMessage(`{"attempts":[{"mode":"edit","edits":[{"path":"a.txt","content":"` + content + `"}]}]}`)
	var refusal *g.Refusal
	if _, err := normalizeSettings("fake", raw); !errors.As(err, &refusal) || refusal.Code != "oversized" || refusal.Field != "settings" {
		t.Fatal("canonical settings overflow not typed", err)
	}
}
