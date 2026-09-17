package inference

import (
	"bytes"
	"encoding/json"
)

func (u *usageParser) openai(data []byte, event string) {
	if u.protocol == "chat_completions" && bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		u.terminal = true
		return
	}
	type usage struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Input      *int64 `json:"input_tokens"`
		Output     *int64 `json:"output_tokens"`
	}
	var v struct {
		Type     string `json:"type"`
		Usage    *usage `json:"usage"`
		Response *struct {
			Status string `json:"status"`
			Usage  *usage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &v) != nil {
		return
	}
	observed := v.Usage
	if u.protocol == "responses" {
		kind := v.Type
		if kind == "" {
			kind = event
		}
		switch kind {
		case "response.completed", "response.failed", "response.incomplete":
			u.terminal = v.Response != nil && "response."+v.Response.Status == kind
		}
		if v.Response != nil {
			observed = v.Response.Usage
		}
	}
	if observed == nil {
		return
	}
	prompt, completion := observed.Prompt, observed.Completion
	if u.protocol == "responses" {
		prompt, completion = observed.Input, observed.Output
	}
	if validTokens(prompt) {
		u.prompt, u.havePrompt = *prompt, true
	}
	if validTokens(completion) {
		u.completion, u.haveCompletion = *completion, true
	}
}
