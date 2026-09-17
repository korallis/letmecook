package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskTotalEnvelopeBoundary(t *testing.T) {
	input := TaskInput{Version: Version, MessageID: IntentID("size-test", "task"), Repository: "fixture", BaseCommit: strings.Repeat("a", 40), Criteria: []Criterion{{ID: "c1", Text: "passes"}}, Paths: []string{"file"}, Operations: []string{"read"}, Harness: "fake", Settings: json.RawMessage(`{"attempts":[{"mode":"noop","edits":[]}]}`)}
	raw, _ := json.Marshal(input)
	input.Brief = strings.Repeat("x", 65536-len(raw))
	if _, err := Normalize(input); err != nil {
		t.Fatal("exact envelope boundary", err)
	}
	input.Brief += "x"
	if _, err := Normalize(input); err == nil {
		t.Fatal("oversized total envelope accepted")
	}
	input.Brief = strings.Repeat("x", 65536)
	if _, err := Normalize(input); err == nil {
		t.Fatal("individual brief bound mistaken for total capacity")
	}
}
