package inference

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayConfig(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gateway.json")
	// This reference deliberately does not exist. Loading config must never read it.
	body := `{"version":"gateway-config-v1","gateway_id":"local","base_url":"https://gateway.example/v1","credential_ref":{"kind":"file","path":"` + filepath.Join(dir, "credential") + `"},"protocols":["openai-chat"],"models":["model-a"]}`
	load := func(b string) (Gateway, error) {
		t.Helper()
		if err := os.WriteFile(file, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
		return LoadGatewayConfig(file)
	}
	got, err := load(body)
	if err != nil || got.ID != "local" || len(got.ProfileDigest) != 64 {
		t.Fatalf("config: %+v %v", got, err)
	}
	var value any
	json.Unmarshal([]byte(body), &value)
	pretty, _ := json.MarshalIndent(value, "", "  ")
	again, err := load(string(pretty))
	if err != nil || again.ProfileDigest != got.ProfileDigest {
		t.Fatal("formatting changed canonical profile digest")
	}
	for name, bad := range map[string]string{
		"http":             strings.Replace(body, "https://", "http://", 1),
		"userinfo":         strings.Replace(body, "https://", "https://user:secret@", 1),
		"query":            strings.Replace(body, "/v1\"", "/v1?secret=x\"", 1),
		"fragment":         strings.Replace(body, "/v1\"", "/v1#x\"", 1),
		"version":          strings.Replace(body, "gateway-config-v1", "gateway-config-v2", 1),
		"unknown":          strings.Replace(body, `"gateway_id":`, `"unknown":false,"gateway_id":`, 1),
		"duplicate":        strings.Replace(body, `"gateway_id":`, `"gateway_id":"other","gateway_id":`, 1),
		"null":             strings.Replace(body, `["model-a"]`, `null`, 1),
		"empty-models":     strings.Replace(body, `["model-a"]`, `[]`, 1),
		"duplicate-models": strings.Replace(body, `["model-a"]`, `["model-a","model-a"]`, 1),
		"empty-protocols":  strings.Replace(body, `["openai-chat"]`, `[]`, 1),
		"credential-kind":  strings.Replace(body, `"kind":"file"`, `"kind":"env"`, 1),
		"relative-path":    strings.Replace(body, filepath.Join(dir, "credential"), "credential", 1),
		"unknown-nested":   strings.Replace(body, `"kind":"file"`, `"secret":"hidden","kind":"file"`, 1),
		"trailing":         body + `{}`,
		"oversized":        strings.Repeat(" ", MaxGatewayConfigBytes) + body,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(bad); !errors.Is(err, ErrGatewayConfig) {
				t.Fatal("admitted invalid config", err)
			}
		})
	}
	if _, err := Start(context.Background(), Scope{}, nil); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("boundary stub launched")
	}
}
