package execclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"

	w "github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
)

type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// TaskInput mirrors the S1 input route; the canonical brief hash binds settings.
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

func (v TaskInput) Digest() (string, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(v.Settings, &settings); err != nil || settings == nil {
		return "", errors.New("settings must be an object")
	}
	var canonical any
	dec := json.NewDecoder(bytes.NewReader(v.Settings))
	dec.UseNumber()
	if err := dec.Decode(&canonical); err != nil {
		return "", err
	}
	b, err := json.Marshal(struct {
		Brief      string      `json:"brief"`
		Criteria   []Criterion `json:"criteria"`
		Paths      []string    `json:"paths"`
		Operations []string    `json:"operations"`
		Harness    string      `json:"harness"`
		Settings   any         `json:"settings"`
	}{v.Brief, v.Criteria, v.Paths, v.Operations, v.Harness, canonical})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func (v TaskInput) Validate(d store.Dispatch) error {
	digest, err := v.Digest()
	if err != nil {
		return err
	}
	if v.Version != w.Version || v.DispatchID != d.ID || v.TaskID != d.Request.TaskID || v.Repository != d.Request.Envelope.Repository || v.BaseCommit != d.Request.Envelope.BaseCommit || v.BriefSHA256 != digest || digest != d.Request.Envelope.Brief.SHA256 || v.Harness != d.Decision.Selected.Harness || !reflect.DeepEqual(v.Paths, d.Request.Envelope.Paths) || !reflect.DeepEqual(v.Operations, d.Request.Envelope.Operations) {
		return errors.New("assignment input digest mismatch")
	}
	return nil
}
func (c *Client) Input(ctx context.Context, id string) (TaskInput, error) {
	var out TaskInput
	err := c.json(ctx, "GET", "/input?dispatch_id="+url.QueryEscape(id), nil, &out)
	return out, err
}
