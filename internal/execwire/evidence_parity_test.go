package execwire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
)

// TestEvidenceMirrorsStoreRuntimeEvidence keeps the wire evidence and the
// store's RuntimeEvidence byte-identical so exit evidence never loses zero
// values on either side of the channel.
func TestEvidenceMirrorsStoreRuntimeEvidence(t *testing.T) {
	wire, err := json.Marshal(execwire.Evidence{Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(store.RuntimeEvidence{Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) != string(stored) {
		t.Fatalf("wire %s != store %s", wire, stored)
	}
	for _, field := range []string{`"code":0`, `"pgid_empty":false`, `"stream_through":0`, `"observed_unix_ns":0`, `"pid":0`, `"pgid":0`, `"start_unix_ns":0`, `"boundary_port":0`, `"guardian_pid":0`} {
		if !strings.Contains(string(wire), field) {
			t.Fatalf("missing %s in %s", field, wire)
		}
	}
}
