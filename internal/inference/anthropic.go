package inference

import "encoding/json"

func (u *usageParser) anthropic(data []byte, event string) {
	type usage struct {
		Input       *int64 `json:"input_tokens"`
		Output      *int64 `json:"output_tokens"`
		CacheRead   *int64 `json:"cache_read_input_tokens"`
		CacheCreate *int64 `json:"cache_creation_input_tokens"`
	}
	var v struct {
		Type    string `json:"type"`
		Usage   *usage `json:"usage"`
		Message *struct {
			Usage *usage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &v) != nil {
		return
	}
	kind := v.Type
	if kind == "" {
		kind = event
	}
	if kind == "message_stop" {
		u.terminal = true
	}
	observed := v.Usage
	if kind == "message_start" && v.Message != nil {
		observed = v.Message.Usage
	}
	if observed == nil {
		return
	}
	if validTokens(observed.Input) {
		total := *observed.Input
		if validTokens(observed.CacheRead) {
			total += *observed.CacheRead
		}
		if validTokens(observed.CacheCreate) {
			total += *observed.CacheCreate
		}
		if total <= 9007199254740991 {
			u.prompt, u.havePrompt = total, true
		}
	}
	// message_delta is cumulative output usage, not a delta to add twice.
	if validTokens(observed.Output) {
		u.completion, u.haveCompletion = *observed.Output, true
	}
}
