package opencode

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/inference/protocoljson"
)

var ErrEvent = errors.New("opencode_event_malformed")

// ParseEvent validates and normalizes a native JSON line. It observes token
// counts; authoritative per-request accounting remains the inference boundary.
func ParseEvent(line []byte) (harness.Event, error) {
	var envelope map[string]json.RawMessage
	if len(line) == 0 || len(line) > 64<<20 || protocoljson.Decode(line, &envelope, len(line)) != nil || envelope == nil {
		return harness.Event{}, ErrEvent
	}
	var kind string
	var timestamp int64
	if json.Unmarshal(envelope["type"], &kind) != nil || !validLabel(kind, 128) {
		return harness.Event{}, ErrEvent
	}
	if raw, ok := envelope["timestamp"]; ok {
		var value *int64
		if json.Unmarshal(raw, &value) != nil || value == nil || *value < 0 || *value > 253402300799999 {
			return harness.Event{}, ErrEvent
		}
		timestamp = *value // time.Time must remain JSON-encodable (year <= 9999).
	}
	var part struct {
		Tool   string `json:"tool"`
		Reason string `json:"reason"`
		Text   string `json:"text"`
		State  struct {
			Status string `json:"status"`
		} `json:"state"`
		Tokens *struct {
			Input, Output *int64
			Cache         struct{ Read, Write int64 }
		} `json:"tokens"`
	}
	if raw, ok := envelope["part"]; ok && json.Unmarshal(raw, &part) != nil {
		return harness.Event{}, ErrEvent
	}
	event := harness.Event{Kind: harness.Activity, At: time.UnixMilli(timestamp).UTC(), Summary: "opencode " + kind, Raw: boundedRaw(line)}
	switch kind {
	case "step_start":
		event.Kind = harness.Started
		event.Summary = "step started"
	case "tool_use":
		if !validLabel(part.Tool, 128) || !validLabel(part.State.Status, 64) {
			return harness.Event{}, ErrEvent
		}
		if part.State.Status == "pending" || part.State.Status == "running" {
			event.Kind = harness.ToolRequested
		}
		event.Summary = "tool " + part.Tool + " " + part.State.Status
	case "step_finish":
		event.Summary = "step finished: " + part.Reason
		if part.Reason == "stop" {
			event.Kind = harness.Completed
		}
	case "text":
		event.Summary = part.Text
	case "error":
		event.Kind = harness.Failed
		event.Summary = "opencode error"
	case "permission", "permission.asked", "approval_required":
		event.Kind = harness.ApprovalRequired
		event.Summary = "approval required"
	}
	if part.Tokens != nil {
		t := part.Tokens
		for _, value := range []*int64{t.Input, t.Output} {
			if value != nil && (*value < 0 || *value > 9007199254740991) {
				return harness.Event{}, ErrEvent
			}
		}
		if t.Cache.Read < 0 || t.Cache.Write < 0 || t.Cache.Read > 9007199254740991-t.Cache.Write {
			return harness.Event{}, ErrEvent
		}
		if t.Input != nil && t.Output != nil {
			if *t.Input > 9007199254740991-t.Cache.Read-t.Cache.Write {
				return harness.Event{}, ErrEvent
			}
			event.Usage = &harness.Usage{PromptTokens: *t.Input + t.Cache.Read + t.Cache.Write, CompletionTokens: *t.Output, Source: "opencode_tokens"}
		} // Missing/null totals are unknown usage, never observed zero.
	}
	event.Summary = boundedSummary(event.Summary)
	return event, nil
}
func boundedSummary(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) > 4096 {
		s = strings.ToValidUTF8(s[:4096], "") + " [truncated]"
	}
	return s
}
func boundedRaw(raw []byte) json.RawMessage {
	if len(raw) <= harness.MaxRawBytes {
		return append(json.RawMessage(nil), raw...)
	}
	// A valid JSON envelope makes truncation explicit without dropping the prefix.
	truncated, _ := json.Marshal(struct {
		Truncated bool   `json:"truncated"`
		Bytes     int    `json:"bytes"`
		Prefix    string `json:"prefix"`
	}{true, len(raw), string(raw[:8192])})
	return truncated
}
