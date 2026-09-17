package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateTaskWireAndCanonicalSettingsBoundsHTTP(t *testing.T) {
	f := newOwnerFixture(t)
	endpoint, client := f.serve(t)
	for _, name := range []string{"owner-bound", "canonical-settings"} {
		t.Run(name, func(t *testing.T) {
			body := taskBody(f, "bounds-"+name)
			if name == "owner-bound" {
				body["brief"] = ""
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				body["brief"] = strings.Repeat("x", 65536-len(raw))
			} else {
				body["settings"] = json.RawMessage(`{"attempts":[{"mode":"edit","edits":[{"path":"file","content":"` + strings.Repeat("<", 3500) + strings.Repeat("a", 30000) + `"}]}]}`)
			}
			raw, _ := postWorkflow(t, client, endpoint, "/api/v1/tasks", body, 422)
			var failure struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &failure); err != nil || failure.Error != "oversized" {
				t.Fatal("not a typed oversized refusal", string(raw), err)
			}
		})
	}
}
