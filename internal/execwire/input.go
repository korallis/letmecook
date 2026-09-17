package execwire

import "encoding/json"

// Criterion is one acceptance criterion of the task brief as the runner sees it.
type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// TaskInput is the GET /x/v1/input reply: the retained brief behind a dispatch,
// bound to the grant by BriefSHA256 (the canonical digest of {brief, criteria,
// paths, operations, harness, settings} in that key order, settings with sorted
// keys and no whitespace). Slices are always arrays, never null; Settings is
// always a JSON object. It is data for the harness, not authority: the dispatch
// and lease still gate every launch.
type TaskInput struct {
	Version     string          `json:"version"`
	DispatchID  string          `json:"dispatch_id"`
	TaskID      string          `json:"task_id"`
	Repository  string          `json:"repository"`
	BaseCommit  string          `json:"base_commit"`
	BriefSHA256 string          `json:"brief_sha256"`
	Brief       string          `json:"brief"`
	Criteria    []Criterion     `json:"criteria"`
	Paths       []string        `json:"paths"`
	Operations  []string        `json:"operations"`
	Harness     string          `json:"harness"`
	Settings    json.RawMessage `json:"settings"`
}
