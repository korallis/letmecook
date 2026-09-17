// Package readapi defines bounded provisional store/fixture read models.
package readapi

import (
	"fmt"
	"slices"

	p "github.com/korallis/letmecook/schemas/execution"
)

const Version = "read-provisional-v1"
const MaxItems = 50
const MaxBytes = 1 << 20

func MissingCapabilities() []string {
	return []string{"execution", "inference", "artifact_custody", "result_ack", "acceptance", "publication", "merge", "state_import", "sessions"}
}

type Metadata struct {
	Version             string   `json:"version"`
	Mode                string   `json:"mode"`
	MissingCapabilities []string `json:"missing_capabilities"`
	Generation          string   `json:"generation"`
	DaemonBoot          string   `json:"daemon_boot"`
	SchemaVersion       int      `json:"schema_version"`
}
type Attempt struct {
	Identity    p.Identity     `json:"identity"`
	State       p.AttemptState `json:"state"`
	Revision    int64          `json:"revision"`
	Observation p.Observation  `json:"observation"`
}
type Task struct {
	TaskID  string      `json:"task_id"`
	State   p.TaskState `json:"state"`
	Attempt Attempt     `json:"attempt"`
}
type Event struct {
	Sequence int64     `json:"sequence"`
	Revision int64     `json:"revision"`
	Message  p.Message `json:"message"`
}
type Snapshot struct {
	Metadata
	Tasks  []Task  `json:"tasks"`
	Events []Event `json:"events"`
}
type Status struct {
	Metadata
	TaskCount  int64 `json:"task_count"`
	EventCount int64 `json:"event_count"`
}

func (m Metadata) Validate() error {
	if m.Version != Version || !(m.Mode == "fixture-only" && m.SchemaVersion == 1 || m.Mode == "store-only" && (m.SchemaVersion == 2 || m.SchemaVersion == 3 || m.SchemaVersion == 4 || m.SchemaVersion == 5 || m.SchemaVersion == 6 || m.SchemaVersion == 7 || m.SchemaVersion == 8 || m.SchemaVersion == 9)) || !p.ValidID(m.Generation) || !p.ValidID(m.DaemonBoot) || !slices.Equal(m.MissingCapabilities, MissingCapabilities()) {
		return fmt.Errorf("invalid read metadata")
	}
	return nil
}
func (s Status) Validate() error {
	if err := s.Metadata.Validate(); err != nil {
		return err
	}
	if s.TaskCount < 0 || s.TaskCount > p.MaxInteger || s.EventCount < 0 || s.EventCount > p.MaxInteger {
		return fmt.Errorf("invalid counts")
	}
	return nil
}
func (s Snapshot) Validate() error {
	if err := s.Metadata.Validate(); err != nil {
		return err
	}
	if s.Tasks == nil || s.Events == nil || len(s.Tasks) > MaxItems || len(s.Events) > MaxItems {
		return fmt.Errorf("invalid snapshot bounds")
	}
	seen := map[string]bool{}
	for _, t := range s.Tasks {
		a := t.Attempt
		if seen[t.TaskID] || !p.ValidID(t.TaskID) || a.Identity.TaskID != t.TaskID || a.Identity.Generation != s.Generation || !p.ValidID(a.Identity.AttemptID) || a.Identity.Epoch < 1 || a.Identity.Epoch > p.MaxInteger || a.Revision < 1 || a.Revision > p.MaxInteger {
			return fmt.Errorf("invalid task identity")
		}
		seen[t.TaskID] = true
		if !slices.Contains([]p.TaskState{p.TaskReady, p.TaskReconciling, p.TaskVerifying, p.TaskAwaitingReview}, t.State) || !slices.Contains([]p.AttemptState{p.Assigned, p.Starting, p.Running, p.Stopping, p.ResultPending, p.Succeeded, p.Failed, p.Cancelled, p.Expired, p.Unknown}, a.State) {
			return fmt.Errorf("invalid fixture state")
		}
		process := "unknown"
		if s.Mode == "fixture-only" {
			process = "not_started"
		}
		if a.Observation != (p.Observation{Desired: "stop", ConfirmedProcess: process, RemoteWork: "unknown", Quarantined: true}) {
			return fmt.Errorf("invalid fixture observation")
		}
	}
	var previous int64
	for _, e := range s.Events {
		if e.Sequence <= previous || e.Sequence > p.MaxInteger || e.Revision < 1 || e.Revision > p.MaxInteger || e.Message.Identity.Generation != s.Generation || (e.Message.Kind != "assign" && e.Message.Kind != "transition") || p.CheckCurrent(e.Message, e.Message.Identity) != p.OK {
			return fmt.Errorf("invalid event")
		}
		if s.Mode == "store-only" && e.Message.Version != p.FencedVersion {
			return fmt.Errorf("historical message in persistent store")
		}
		previous = e.Sequence
	}
	return nil
}
