package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Every M1 seam refuses with the sentinel until its owning slice lands; nothing
// here may be mistaken for an empty success.
func TestSeamStubsRefuseUntilImplemented(t *testing.T) {
	s, _ := persistent(t)
	var errs []error
	_, err := s.RunnerSession(ctx, "", HelloRecord{})
	errs = append(errs, err)
	_, err = s.ExecutionState(ctx, "", "", "")
	errs = append(errs, err)
	_, err = s.PendingCancels(ctx, "", "")
	errs = append(errs, err)
	_, err = s.ProposeTransition(ctx, "", "", "", p.Message{}, RuntimeEvidence{})
	errs = append(errs, err)
	_, err = s.IssueLease(ctx, "", "", "", "", p.Message{})
	errs = append(errs, err)
	_, err = s.ReportTermination(ctx, "", "", "", c.Evidence{}, BoundaryState{})
	errs = append(errs, err)
	errs = append(errs, s.RecordUsage(ctx, "", "", UsageReport{}))
	_, err = s.BeginUpload(ctx, "", "", "", UploadBegin{})
	errs = append(errs, err)
	_, err = s.RecordUploadedBlob(ctx, "", "", "", "", bytes.NewReader(nil), 0)
	errs = append(errs, err)
	_, err = s.CommitUpload(ctx, "", "", "", "")
	errs = append(errs, err)
	_, err = s.FinalizeAttempt(ctx, "", "", "", Completion{})
	errs = append(errs, err)
	_, err = s.CreateTask(ctx, "", TaskBrief{})
	errs = append(errs, err)
	_, err = s.Task(ctx, "")
	errs = append(errs, err)
	_, err = s.Tasks(ctx, "", 1)
	errs = append(errs, err)
	_, err = s.Eligibility(ctx, "")
	errs = append(errs, err)
	_, err = s.SetPaused(ctx, "", "", true, "", false)
	errs = append(errs, err)
	_, err = s.Paused(ctx)
	errs = append(errs, err)
	_, err = s.Job(ctx, "")
	errs = append(errs, err)
	_, err = s.PutJob(ctx, Job{})
	errs = append(errs, err)
	errs = append(errs, s.RecordOwnerCommand(ctx, OwnerCommand{}))
	_, err = s.OwnerCommand(ctx, "")
	errs = append(errs, err)
	_, err = s.PutGatewayProfile(ctx, "", nil)
	errs = append(errs, err)
	errs = append(errs, s.SnapshotDatabase(ctx, ""))
	release, err := s.PinArtifacts()
	if release != nil {
		t.Fatal("unimplemented pin returned a release")
	}
	errs = append(errs, err)
	_, err = OpenRestored(ctx, "", "", RestoreRecord{}, Options{})
	errs = append(errs, err)
	_, err = s.RestoreHistory(ctx)
	errs = append(errs, err)
	_, err = s.FenceAttempt(ctx, "", "")
	errs = append(errs, err)
	_, err = s.NonTerminalAttempts(ctx)
	errs = append(errs, err)
	_, err = s.ReconcileReport(ctx, "")
	errs = append(errs, err)
	errs = append(errs, s.PutReconcileReport(ctx, ReconcileReport{}))
	_, err = s.ReconciliationInputs(ctx, "")
	errs = append(errs, err)
	if len(errs) != 31 {
		t.Fatalf("stub inventory drifted: %d", len(errs))
	}
	for i, err := range errs {
		if !errors.Is(err, ErrNotImplemented) {
			t.Fatalf("stub %d: %v", i, err)
		}
	}
}

// Reply and record types cross the execution channel, whose decoder rejects
// null. Zero values must marshal without null and round-trip unchanged.
func TestSeamTypesMarshalWithoutNull(t *testing.T) {
	for name, v := range map[string]any{
		"session":     SessionRecord{},
		"state":       ExecutionState{},
		"evidence":    RuntimeEvidence{Kind: "exit"},
		"boundary":    BoundaryState{},
		"termination": TerminationReply{},
		"usage":       UsageReport{},
		"upload":      UploadSession{},
		"blob":        UploadedBlob{},
		"completion":  Completion{},
		"finalize":    FinalizeReply{},
		"brief":       TaskBrief{},
		"task":        Task{},
		"daemon":      DaemonState{},
		"job":         Job{},
		"command":     OwnerCommand{},
		"gateway":     GatewayProfile{},
		"restore":     RestoreEntry{},
		"reconcile":   ReconcileReport{},
		"inputs":      ReconciliationInputs{},
		"observation": RuntimeObservation{},
		"hello":       HelloRecord{},
	} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(name, err)
		}
		if bytes.Contains(raw, []byte("null")) {
			t.Fatalf("%s marshals null: %s", name, raw)
		}
	}
	// The wire's base64 manifest field is the raw manifest on the store side.
	raw, err := json.Marshal(UploadBegin{Manifest: []byte("{}")})
	if err != nil || !bytes.Contains(raw, []byte(`"manifest_base64":"e30="`)) {
		t.Fatal(string(raw), err)
	}
	// Required evidence numbers are present even when zero: an exit code of 0 and
	// a stream watermark of 0 are evidence, never omissions.
	raw, err = json.Marshal(RuntimeEvidence{Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"boundary_port":0`, `"guardian_pid":0`, `"pid":0`, `"pgid":0`, `"start_unix_ns":0`, `"code":0`, `"pgid_empty":false`, `"observed_unix_ns":0`, `"stream_through":0`} {
		if !bytes.Contains(raw, []byte(field)) {
			t.Fatalf("required evidence field omitted: %s in %s", field, raw)
		}
	}
	if bytes.Contains(raw, []byte(`"workspace"`)) || bytes.Contains(raw, []byte(`"nonce"`)) {
		t.Fatalf("optional strings emitted when empty: %s", raw)
	}
}
