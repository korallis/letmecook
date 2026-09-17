//go:build system

package system

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	checks := obj(full["checks"])
	commands := arr(checks["checks"])
	profileMatches := str(checks["id"]) != "" && str(checks["approved_by"]) != "" && str(checks["approval_ref"]) != "" && len(commands) == len(arr(expected["commands"]))
	for i, command := range commands {
		profileMatches = profileMatches && str(obj(command)["name"]) == fmt.Sprintf("%s-%d", str(expected["profile"]), i+1)
	}
	r.s.check(r.t, "trusted verification profile matches fixture", profileMatches, checks)
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

func TestEncodedEvidenceRedaction(t *testing.T) {
	payload := `{"token":"synthetic-secret","path":"/Users/synthetic/private/key","url":"https://private.example/v1"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	clean := obj(redact(object{"native": object{"data": encoded}, "stderr": object{"prefix": encoded}}))
	for _, pair := range []struct{ object, field string }{{"native", "data"}, {"stderr", "prefix"}} {
		decoded := string(decodeBase64(str(obj(clean[pair.object])[pair.field])))
		if strings.Contains(decoded, "synthetic-secret") || strings.Contains(decoded, "/Users/synthetic") || strings.Contains(decoded, "private.example") {
			t.Fatalf("encoded evidence leaked private fields: %s", pair.object)
		}
	}
}

func TestCredentialProbeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, event string
		want        bool
	}{
		{"echo", `{"type":"text","part":{"text":"credential_unreadable"}}`, false},
		{"running", `{"type":"tool_use","part":{"tool":"bash","state":{"status":"running","output":"credential_unreadable"}}}`, false},
		{"tool result", `{"type":"tool_use","part":{"tool":"bash","state":{"status":"completed","output":"credential_unreadable\n"}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := object{"records": []any{object{"native": object{"data": base64.StdEncoding.EncodeToString([]byte(tc.event))}}}}
			if got := credentialProbeEvidence(stream); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
