//go:build system

package system

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceRedaction(t *testing.T) {
	secret := "synthetic-enrollment-secret-not-for-evidence"
	input := object{"token": secret, "stdout": "{\"result\":{\"token\":\"" + secret + "\"}}\n{\"authorization\":\"" + secret + "\"}\n", "stderr": "{\"apiKey\":\"" + secret + "\"}", "nested": []any{object{"credential": secret}}}
	b, err := json.Marshal(redact(input))
	if err != nil || strings.Contains(string(b), secret) || !strings.Contains(string(b), "redacted") {
		t.Fatalf("redaction did not eliminate nested structured secrets: %v", err)
	}
}

func (r *installation) assertVerification(task, expected object) {
	summary := obj(task["verification"])
	full := obj(r.get("/api/v1/verifications/" + str(summary["id"])))
	r.s.check(r.t, "trusted verification profile matches fixture", str(obj(full["checks"])["id"]) == str(expected["profile"]), full["checks"])
	evidence := arr(full["evidence"])
	for _, v := range arr(expected["commands"]) {
		command := obj(v)
		index := int(num(command["index"]))
		if !r.s.check(r.t, "trusted command evidence exists", index >= 0 && index < len(evidence), command) {
			continue
		}
		e := obj(evidence[index])
		passed := e["exit_code"] != nil && num(e["exit_code"]) == 0 && e["refusal"] == nil && str(e["failure"]) == ""
		r.s.check(r.t, "per-command fixture verification outcome", passed == (str(command["expect"]) == "pass"), object{"expected": command, "evidence": e})
	}
}
