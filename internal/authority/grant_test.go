package authority

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func gatewayEnvelope(t *testing.T) Envelope {
	t.Helper()
	hash := strings.Repeat("a", 64)
	rev := Revision{Number: 1, SHA256: hash}
	route := Route{RouteRef: "worker-dev", ProfileRef: "gateway-local", RouteRevision: 1, Policy: rev, RouterBuild: hash, GraphDigest: hash, Evidence: rev, Harness: "fake", Protocol: "responses", SettingsDigest: hash, Isolation: "macos-sandbox-exec-dev", LimitsProfile: "gateway-local-bounds-v1", LimitsAuthority: "operator", Targets: []Target{{Provider: "gateway-local", Model: "gpt-6-astra", Billing: "gateway-managed"}}}
	now := time.Now().UnixMilli()
	return Envelope{
		Repository: "greeting", BaseCommit: strings.Repeat("b", 40), Brief: rev, Plan: rev, RouteDecision: rev,
		CriterionIDs: []string{"c1"}, TaskKinds: []string{"code"}, Paths: []string{"greeting.txt"}, Operations: []string{"read", "verify", "write"}, Systems: []string{}, Runners: []string{"00000000-0000-4000-8000-000000000001"}, Routes: []Route{route}, Selection: "pinned",
		Budgets: Budgets{Requests: 5, Attempts: 1, Subattempts: 2, Retries: 0, Concurrency: 1, RequestBytes: 1024, ResponseBytes: 4096, TotalMS: 60000, AttemptMS: 30000, FirstOutputMS: 5000, IdleMS: 5000}, NotBeforeMS: now - 1000, ExpiresMS: now + 3600000,
	}
}

func refusal(t *testing.T, err error, code, field string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Code != code || r.Field != field {
		t.Fatalf("want %s/%s, got %v", code, field, err)
	}
}

func TestGatewayLimitsProfileAndBilling(t *testing.T) {
	e := gatewayEnvelope(t)
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	cost := int64(0)
	for name, mutate := range map[string]func(*Envelope){
		"output-tokens":       func(e *Envelope) { e.Budgets.ProviderOutputTokens = 1 },
		"cost-cap":            func(e *Envelope) { e.Budgets.ProviderCostMicros = &cost },
		"subscription-target": func(e *Envelope) { e.Routes[0].Targets[0].Billing = "subscription" },
		"metered-target":      func(e *Envelope) { e.Routes[0].Targets[0].Billing = "metered" },
	} {
		bad := gatewayEnvelope(t)
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatal(name, "accepted")
		} else {
			var r *Refusal
			if !errors.As(err, &r) || r.Code != "incompatible_route_policy" {
				t.Fatal(name, err)
			}
		}
	}
	// gateway-managed billing is only meaningful under the gateway profile.
	for _, profile := range []string{"strict-provider-output-v1", "native-subscription-local-v1"} {
		bad := gatewayEnvelope(t)
		bad.Routes[0].LimitsProfile = profile
		bad.Budgets.ProviderOutputTokens = 1
		if profile == "native-subscription-local-v1" {
			bad.Budgets.ProviderOutputTokens = 0
		}
		refusal(t, bad.Validate(), "incompatible_route_policy", "billing")
	}
	unknown := gatewayEnvelope(t)
	unknown.Routes[0].Targets[0].Billing = "prepaid"
	refusal(t, unknown.Validate(), "malformed", "provider_model_billing")
	// No new Route fields: the wire shape of a historical grant is byte-identical.
	raw, err := json.Marshal(Route{})
	if err != nil || string(raw) != `{"route_ref":"","profile_ref":"","route_revision":0,"policy":{"number":0,"sha256":""},"router_build":"","graph_digest":"","evidence":{"number":0,"sha256":""},"harness":"","protocol":"","settings_digest":"","isolation":"","limits_profile":"","limits_authority":"","targets":null}` {
		t.Fatalf("route shape changed: %s %v", raw, err)
	}
}
