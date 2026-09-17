package execwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/korallis/letmecook/internal/closedjson"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Decode accepts one closed object of at most 64 KiB. Domain validations (IDs,
// evidence, leases, authority) belong to the corresponding store transaction.
func Decode(data []byte, dst any) error {
	if len(data) > MaxBytes {
		return p.Oversized
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return p.Malformed
	}
	if err := closedjson.Decode(data, dst, MaxBytes, map[string]bool{"provider_cost_micros": true, "prior_stop_by_ms": true, "request_to_ack_ns": true}); err != nil {
		return p.Malformed
	}
	var header struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &header) != nil {
		return p.Malformed
	}
	if hasVersion(dst) && header.Version != Version {
		return p.UnknownVersion
	}
	return nil
}
func hasVersion(v any) bool {
	typ := reflect.TypeOf(v)
	if typ == nil {
		return false
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return false
	}
	_, ok := typ.FieldByName("Version")
	return ok
}

// Encode checks that its output is accepted by the same closed, bounded decoder.
// Nil required arrays must be supplied as empty slices, never JSON null.
func Encode(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, p.Malformed
	}
	typ := reflect.TypeOf(value)
	if typ == nil {
		return nil, p.Malformed
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if err := Decode(b, reflect.New(typ).Interface()); err != nil {
		return nil, err
	}
	return b, nil
}

// ExpiryStopID derives a stable UUIDv4-shaped intent from the last lease nonce.
// It is deterministic, not a source of random IDs or a proof of lease ownership.
func ExpiryStopID(nonce string) string { return derivedID("lease-expiry:" + nonce) }

// LocalStopCauses are the runner-originated stop causes a supervisor may report
// without an owner stop or a lease expiry: it shut down, a launch failed after
// acceptance, containment could not be confirmed, or its local policy drifted.
var LocalStopCauses = []string{"runner_shutdown", "launch_failed", "containment_unconfirmed", "local_policy_drift"}

// LocalStopID derives the deterministic stop identity for a runner-originated
// stop of one attempt and cause, so replay and the daemon's latch agree.
func LocalStopID(attemptID, cause string) string {
	return derivedID("local-stop:" + attemptID + ":" + cause)
}

func derivedID(seed string) string {
	b := sha256.Sum256([]byte(seed))
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
