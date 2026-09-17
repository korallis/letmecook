package execwire_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	w "github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestEnvelopeRoundTrips(t *testing.T) {
	for _, value := range []any{
		w.Hello{Version: w.Version, Journals: []w.Journal{}}, w.Session{},
		w.State{StopTargets: []c.Target{}}, w.Inbox{Assignments: []w.Dispatch{}, Cancels: []p.Message{}},
		w.MessageEnvelope{Version: w.Version}, w.LeaseEnvelope{Version: w.Version},
		w.StreamBatch{Version: w.Version, Records: []runstream.Record{}}, w.StreamAck{},
		w.UploadBegin{Version: w.Version}, w.UploadSession{Missing: []w.MissingBlob{}},
		w.CommitReply{}, w.Completion{Version: w.Version}, w.Usage{Version: w.Version, Receipts: []inference.Receipt{}}, w.ErrorBody{Version: w.Version},
	} {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			b, err := w.Encode(value)
			if err != nil {
				t.Fatal(err)
			}
			dst := reflect.New(reflect.TypeOf(value))
			if err := w.Decode(b, dst.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(value, dst.Elem().Interface()) {
				t.Fatalf("round trip changed %#v", value)
			}
			bad := append([]byte(`{"unknown":true,`), b[1:]...)
			if err := w.Decode(bad, dst.Interface()); err == nil {
				t.Fatal("unknown field admitted")
			}
		})
	}
}
func TestClosedBodies(t *testing.T) {
	for _, bad := range []string{
		`null`, `[]`, `{"version":"execution-channel-provisional-v1","journals":null}`,
		`{"version":"execution-channel-provisional-v1","journals":[null]}`,
		`{"version":"execution-channel-provisional-v1","journals":[{"unknown":true}]}`,
		`{"version":"execution-channel-provisional-v1","journals":[{"identity":{"generation":null}}]}`,
		`{"version":"execution-channel-provisional-v1","message_id":null}`,
		`{"version":"execution-channel-provisional-v1","version":"execution-channel-provisional-v1"}`,
		`{"version":"other"}`, `{}`, `{} {}`, "{\"version\":\"\xff\"}",
		strings.Repeat(" ", w.MaxBytes) + `{}`,
	} {
		var got w.Hello
		if err := w.Decode([]byte(bad), &got); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := w.Encode(w.Hello{Version: w.Version}); err == nil {
		t.Fatal("encoded null required array")
	}
	if _, err := w.Encode(w.ErrorBody{Version: w.Version, Detail: strings.Repeat("a", w.MaxBytes)}); err != p.Oversized {
		t.Fatalf("encode bound: %v", err)
	}
	id := w.ExpiryStopID("nonce")
	if !p.ValidID(id) || id != w.ExpiryStopID("nonce") || id == w.ExpiryStopID("other") {
		t.Fatal("unstable expiry intent")
	}
	if id != "8953a060-4323-4dfc-8ed8-7f1c59771ae7" {
		t.Fatalf("expiry derivation changed: %s", id)
	}
}
func TestStoreDispatchJSONIdentical(t *testing.T) {
	// Non-nil empty collections preserve closed JSON while legacy nullable budgets
	// remain null. This fixture exercises every nested collection in a dispatch.
	env := g.Envelope{CriterionIDs: []string{}, TaskKinds: []string{}, Paths: []string{}, Operations: []string{}, Systems: []string{}, Runners: []string{}, Routes: []g.Route{}}
	route := g.Route{Targets: []g.Target{}}
	facts := sc.Eligibility{LocalEnvelope: env, Route: route, Paths: []sc.PathEvidence{}, Capabilities: []string{}, Isolation: sc.IsolationProfile{Controls: []string{}}}
	decision := sc.Decision{Assessment: sc.Assessment{Required: []string{}, Preferences: []string{}, EvidenceRefs: []string{}, Unknowns: []string{}}, Selected: route, RankedAlternatives: []string{}}
	original := store.Dispatch{DispatchRequest: store.DispatchRequest{ID: "intent", Request: g.Request{Envelope: env}, Decision: decision}, Facts: facts}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var wire w.Dispatch
	if err := w.Decode(b, &wire); err != nil {
		t.Fatalf("store dispatch decode: %v\n%s", err, b)
	}
	after, err := w.Encode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, after) {
		t.Fatalf("dispatch JSON changed\n%s\n%s", b, after)
	}
	var back store.Dispatch
	if err := json.Unmarshal(after, &back); err != nil || !reflect.DeepEqual(original, back) {
		t.Fatal("store dispatch round trip", err)
	}
}
