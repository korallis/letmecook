package execclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"

	w "github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
)

type Criterion = w.Criterion

// TaskInput uses the definitive wire shape and adds client-side validation.
type TaskInput w.TaskInput

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
	settingsJSON, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	criteria := make([]store.Criterion, len(v.Criteria))
	for n, c := range v.Criteria {
		criteria[n] = store.Criterion{ID: c.ID, Text: c.Text}
	}
	return store.BriefDigest(store.TaskBrief{Brief: v.Brief, Criteria: criteria,
		Paths: v.Paths, Operations: v.Operations, Harness: v.Harness, Settings: settingsJSON}), nil
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
