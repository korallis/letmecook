package inference

import (
	"bytes"
	"encoding/json"
	"github.com/korallis/letmecook/internal/closedjson"
	"mime"
	"strings"
)

const maxUsageFrame = 1 << 20

// This parser is observation-only. Its bounded buffers never change forwarded
// bytes and malformed/absent usage is marked unknown, not billed as zero.
type usageParser struct {
	protocol                                       string
	sse                                            bool
	pending, data                                  []byte
	jsonBody                                       []byte
	event                                          string
	overflow, terminal, havePrompt, haveCompletion bool
	prompt, completion                             int64
}

func newUsageParser(protocol, contentType string) *usageParser {
	media, _, _ := mime.ParseMediaType(contentType)
	return &usageParser{protocol: protocol, sse: media == "text/event-stream"}
}
func (u *usageParser) feed(b []byte) {
	if !u.sse {
		if len(u.jsonBody)+len(b) > maxUsageFrame {
			u.overflow = true
			u.jsonBody = nil
			return
		}
		if !u.overflow {
			u.jsonBody = append(u.jsonBody, b...)
		}
		return
	}
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if len(u.pending)+len(b) <= maxUsageFrame && !u.overflow {
				u.pending = append(u.pending, b...)
			} else {
				u.pending = nil
				u.overflow = true
			}
			return
		}
		if len(u.pending)+i > maxUsageFrame {
			u.overflow = true
		}
		if !u.overflow {
			u.pending = append(u.pending, b[:i]...)
			u.line(strings.TrimSuffix(string(u.pending), "\r"))
		} else if i == 0 && len(u.pending) == 0 {
			u.data = nil
			u.event = ""
			u.overflow = false
		}
		u.pending = nil
		b = b[i+1:]
	}
}
func (u *usageParser) line(line string) {
	if line == "" {
		if len(u.data) > 0 {
			u.observe(bytes.TrimSuffix(u.data, []byte("\n")), u.event)
		}
		u.data, u.event = nil, ""
		return
	}
	if strings.HasPrefix(line, "event:") {
		u.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		return
	}
	if strings.HasPrefix(line, "data:") {
		value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		if len(u.data)+len(value)+1 > maxUsageFrame {
			u.overflow = true
			u.data = nil
			return
		}
		u.data = append(u.data, value...)
		u.data = append(u.data, '\n')
	}
}
func (u *usageParser) finish(status int) bool {
	if u.sse {
		// Do not accept an unterminated data frame as a terminal SSE event.
		if status >= 300 {
			return true
		}
		return status != 202 && u.terminal
	}
	if !u.overflow {
		u.observe(u.jsonBody, "")
	}
	if status >= 300 {
		return true
	}
	if status == 202 {
		return false
	}
	if u.protocol == "responses" {
		var body struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(u.jsonBody, &body) != nil {
			return false
		}
		return body.Status == "completed" || body.Status == "failed" || body.Status == "incomplete"
	}
	return true // A fully drained synchronous JSON HTTP result.
}
func (u *usageParser) observe(data []byte, event string) {
	if !(u.protocol == "chat_completions" && bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]"))) {
		var object map[string]json.RawMessage
		nullable := map[string]bool{"content": true, "logprobs": true, "finish_reason": true, "stop_reason": true, "stop_sequence": true, "system_fingerprint": true, "service_tier": true, "error": true, "incomplete_details": true, "previous_response_id": true, "instructions": true, "temperature": true, "top_p": true, "metadata": true, "parallel_tool_calls": true, "user": true, "max_output_tokens": true, "top_logprobs": true, "reasoning_effort": true, "effort": true, "summary": true, "encrypted_content": true, "obfuscation": true}
		if closedjson.Decode(data, &object, maxUsageFrame, nullable) != nil || object == nil {
			return
		}
	}

	if u.protocol == "messages" {
		u.anthropic(data, event)
	} else {
		u.openai(data, event)
	}
}
func (u *usageParser) apply(r *Receipt) {
	if u.havePrompt {
		r.PromptTokens = u.prompt
	}
	if u.haveCompletion {
		r.CompletionTokens = u.completion
	}
	if u.havePrompt && u.haveCompletion {
		r.Source = "gateway_usage"
	} else {
		r.Source = "gateway_usage_unknown"
	}
}
func validTokens(v *int64) bool { return v != nil && *v >= 0 && *v <= 9007199254740991 }
