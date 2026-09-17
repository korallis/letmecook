//go:build live

package inference

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

// This opt-in smoke test performs one bounded inference, not repository
// execution. It never prints the endpoint, credential reference, token, response
// body or transport error. The real worker/isolation workflow belongs to S7.
func TestLiveGatewayBoundary(t *testing.T) {
	if os.Getenv("GAFFER_LIVE_GATEWAY") != "1" {
		t.Skip("requires GAFFER_LIVE_GATEWAY=1 and an operator gateway config path")
	}
	path := os.Getenv("GAFFER_GATEWAY_CONFIG")
	if path == "" {
		t.Fatal("gateway config path is required")
	}
	gateway, err := LoadGatewayConfig(path)
	if err != nil {
		t.Fatal("gateway configuration refused")
	}
	if len(gateway.Protocols) == 0 || len(gateway.Models) == 0 {
		t.Fatal("empty gateway scope")
	}
	protocol, model := gateway.Protocols[0], gateway.Models[0]
	token := rand.Text() + rand.Text()
	scope := Scope{Identity: p.Identity{Generation: requestID(), TaskID: requestID(), AttemptID: requestID(), Epoch: 1}, Token: token, Models: []string{model}, Protocols: []string{protocol}, Gateway: gateway, Limits: Limits{Requests: 1, RequestBytes: 65536, ResponseBytes: 1 << 20, RequestTimeout: 60 * time.Second}}
	sink := &receiptJournal{file: filepath.Join(t.TempDir(), "live-receipts.jsonl")}
	boundary, err := Start(context.Background(), scope, sink)
	if err != nil {
		t.Fatal("gateway boundary could not start")
	}
	defer boundary.Close(context.Background())
	body := map[string]any{"model": model, "stream": false}
	switch protocol {
	case "chat_completions":
		body["messages"] = []map[string]string{{"role": "user", "content": "Reply with exactly ok."}}
		body["max_tokens"] = 128
	case "responses":
		body["input"] = "Reply with exactly ok."
		body["max_output_tokens"] = 128
		body["store"] = false
	case "messages":
		body["messages"] = []map[string]string{{"role": "user", "content": "Reply with exactly ok."}}
		body["max_tokens"] = 128
	default:
		t.Fatal("configured protocol is unsupported")
	}
	raw, _ := json.Marshal(body)
	request, err := http.NewRequest("POST", "http://"+boundary.Addr()+protocolPaths[protocol], bytes.NewReader(raw))
	if err != nil {
		t.Fatal("local request construction failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 65 * time.Second}).Do(request)
	if err != nil {
		t.Fatal("local boundary request failed; details withheld")
	}
	n, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	state := boundary.Close(context.Background())
	_, receipts := sink.snapshot()
	if readErr != nil || n > 1<<20 || response.StatusCode != 200 || !state.Quiescent || state.Reservations != 1 || state.TerminalReceipts != 1 || len(receipts) != 1 {
		t.Fatalf("redacted live result: status=%d reservations=%d terminal=%d quiescent=%t", response.StatusCode, state.Reservations, state.TerminalReceipts, state.Quiescent)
	}
	if receipts[0].Model != model || receipts[0].Protocol != protocol || !receipts[0].Terminal {
		t.Fatal("live receipt did not match the pinned scope")
	}
	t.Logf("redacted live result: status=%d reservations=%d terminal=%d quiescent=%t usage_source=%s", response.StatusCode, state.Reservations, state.TerminalReceipts, state.Quiescent, receipts[0].Source)
}
