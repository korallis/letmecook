package opencode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/korallis/letmecook/internal/harness"
)

func TestCapturedParserGoldens(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "tests", "fixtures", "harness", "opencode-1.18.31")
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("/Users/")) || bytes.Contains(raw, []byte("/private/tmp/")) || bytes.Contains(raw, []byte("https://")) {
		t.Fatal("fixture not redacted")
	}
	type usage struct {
		Prompt     int64  `json:"prompt_tokens"`
		Completion int64  `json:"completion_tokens"`
		Source     string `json:"source"`
	}
	type golden struct {
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
		Usage   *usage `json:"usage,omitempty"`
	}
	var got, want []golden
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		event, err := ParseEvent(scanner.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if len(event.Raw) > harness.MaxRawBytes || !json.Valid(event.Raw) {
			t.Fatal("invalid raw")
		}
		g := golden{Kind: event.Kind, Summary: event.Summary}
		if event.Usage != nil {
			g.Usage = &usage{event.Usage.PromptTokens, event.Usage.CompletionTokens, event.Usage.Source}
		}
		got = append(got, g)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "parser-golden.json"))
	if err != nil || json.Unmarshal(raw, &want) != nil {
		t.Fatal("golden unavailable", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
func TestParserRejectsAmbiguousEventsAndBoundsRaw(t *testing.T) {
	for _, raw := range []string{`{}`, `{"type":"text","type":"error"}`, `{"type":"text","part":{"text":"ok","text":"bad"}}`, `{"type":"text","timestamp":null}`, `{"type":"step_finish","part":{"tokens":{"input":-1,"output":1}}}`, `{"type":"tool_use","part":{"tool":"bash"}}`, `{"type":"text"}{}`} {
		if _, err := ParseEvent([]byte(raw)); err == nil {
			t.Fatal("malformed event admitted")
		}
	}
	large, _ := json.Marshal(map[string]any{"type": "text", "timestamp": 1, "part": map[string]string{"text": strings.Repeat("x", 60<<10)}})
	event, err := ParseEvent(large)
	if err != nil || len(event.Raw) > harness.MaxRawBytes || !json.Valid(event.Raw) || !bytes.Contains(event.Raw, []byte(`"truncated":true`)) {
		t.Fatal("raw bound", err)
	}
	event, err = ParseEvent([]byte(`{"type":"future_event","timestamp":1,"future":{"safe":"data"}}`))
	if err != nil || event.Kind != harness.Activity {
		t.Fatal("unknown event not retained")
	}
	event, err = ParseEvent([]byte(`{"type":"tool_use","part":{"tool":"bash","state":{"status":"running"}}}`))
	if err != nil || event.Kind != harness.ToolRequested {
		t.Fatal("tool request not normalized")
	}
}

func TestParserAcceptsNullableDataWithoutInventingUsage(t *testing.T) {
	for _, raw := range []string{
		`{"type":"tool_use","timestamp":1,"part":{"tool":"lookup","state":{"status":"completed","output":{"result":null,"items":[null,{"value":null}]}},"schema":{"default":null,"enum":[null,"ok"]}}}`,
		`{"type":"step_finish","part":{"reason":"stop","tokens":{"input":null,"output":1},"future":null}}`,
		`{"type":"step_finish","part":{"reason":"stop","tokens":{"output":1}}}`,
		`{"type":"future_event","future":[null,{"anything":null}]}`,
	} {
		event, err := ParseEvent([]byte(raw))
		if err != nil || event.Usage != nil || string(event.Raw) != raw {
			t.Fatal("nullable data refused, rewritten or claimed as usage", err)
		}
	}
	for _, raw := range []string{
		`{"type":null}`, `{"type":"text","timestamp":null}`, `{"type":"text","timestamp":253402300800000}`,
		`{"type":"tool_use","part":{"tool":null,"state":{"status":"running"}}}`,
		`{"type":"tool_use","part":{"tool":"lookup","state":{"status":null}}}`,
		`{"type":"text","part":{"output":{"x":null,"x":1}}}`,
	} {
		if _, err := ParseEvent([]byte(raw)); err == nil {
			t.Fatal("interpreted null, invalid timestamp or nested duplicate accepted")
		}
	}
}
