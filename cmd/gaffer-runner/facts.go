package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/runner"
	sc "github.com/korallis/letmecook/internal/scheduler"
)

func facts(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("facts", flag.ContinueOnError)
	state := f.String("state-dir", "", "running supervisor state")
	policy := f.String("policy", "", "trusted local policy template")
	gatewayFile := f.String("gateway-config", "", "gateway profile override")
	caFile := f.String("gateway-ca", "", "explicit test gateway CA PEM")
	model := f.String("model", "", "gateway model")
	if err := f.Parse(args); err != nil {
		return err
	}
	if !filepath.IsAbs(*state) {
		return errors.New("absolute state-dir required")
	}
	var boot bootRecord
	var cfg config
	b, err := os.ReadFile(filepath.Join(*state, "boot.json"))
	if err != nil {
		return err
	}
	if err = closedjson.Decode(b, &boot, 4096, nil); err != nil {
		return err
	}
	b, err = os.ReadFile(filepath.Join(*state, "serve.json"))
	if err != nil {
		return err
	}
	if err = closedjson.Decode(b, &cfg, 16384, nil); err != nil {
		return err
	}
	if *policy == "" {
		*policy = cfg.Policy
	}
	local, err := loadPolicy(*policy)
	if err != nil {
		return err
	}
	local.RunnerBoot = boot.RunnerBoot
	local.Revision++
	local.RouterAuthenticated = false
	if *gatewayFile != "" {
		cfg.GatewayConfig = *gatewayFile
	}
	route := local.Route
	route.RouteRef = "worker-dev"
	route.Harness = cfg.Harness
	route.Isolation = cfg.Isolation
	route.LimitsProfile = "gateway-local-bounds-v1"
	route.LimitsAuthority = "operator"
	if cfg.GatewayConfig != "" {
		gateway, err := inference.LoadGatewayConfig(cfg.GatewayConfig)
		if err != nil {
			return err
		}
		authenticated, err := probeGateway(ctx, gateway, *caFile)
		if err != nil {
			return err
		}
		local.RouterAuthenticated = authenticated
		route.ProfileRef = gateway.ID
		route.RouterBuild = gateway.ProfileDigest
		route.Protocol = gateway.Protocols[0]
		if *model == "" {
			*model = gateway.Models[0]
		}
		found := false
		for _, m := range gateway.Models {
			found = found || m == *model
		}
		if !found {
			return errors.New("model not in gateway allowlist")
		}
		route.Targets = []g.Target{{Provider: gateway.ID, Model: *model, Billing: "gateway-managed"}}
		graph, _ := json.Marshal(gateway.Models)
		hash := sha256.Sum256(graph)
		route.GraphDigest = hex.EncodeToString(hash[:])
		route.Evidence = g.Revision{Number: local.Revision, SHA256: gateway.ProfileDigest}
	} else {
		route.ProfileRef = "fake-no-gateway"
		route.Targets = []g.Target{{Provider: "fake-no-gateway", Model: "fake", Billing: "gateway-managed"}}
	}
	local.Route = route
	local.LocalEnvelope.Routes = []g.Route{route}
	local.LocalEnvelope.Budgets.ProviderOutputTokens = 0
	local.LocalEnvelope.Budgets.ProviderCostMicros = nil
	local.Paths = []sc.PathEvidence{}
	for _, target := range route.Targets {
		local.Paths = append(local.Paths, sc.PathEvidence{Target: target, Compatible: true, Capabilities: append([]string{}, local.Capabilities...), ContextTokens: 32768, LocalBounds: true})
	}
	profile, err := profileFor(cfg)
	if err != nil {
		return err
	}
	local.Isolation.Supported = false
	local.Isolation.Qualification = profile.Qualification()
	local.Isolation.ID = profile.ID()
	local.Isolation.Kind = profile.Kind()
	local.Isolation.Controls = profile.Controls()
	if profile.Qualification() == isolation.QualificationDevelopment {
		parent, err := os.MkdirTemp(cfg.RepositoryRoot, "facts-probe-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(parent)
		w := isolation.Workspace{Root: filepath.Join(parent, "root"), PrivateHome: filepath.Join(parent, "home"), TempDir: filepath.Join(parent, "tmp"), RuntimeDir: filepath.Join(parent, "runtime")}
		for _, p := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
			if err = os.Mkdir(p, 0700); err != nil {
				return err
			}
		}
		launcher, err := profile.Prepare(ctx, w)
		if err != nil {
			return err
		}
		defer launcher.Cleanup()
		cmd := exec.CommandContext(ctx, "/usr/bin/true")
		cmd.Dir = w.Root
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + w.PrivateHome, "TMPDIR=" + w.TempDir}
		if err = launcher.Wrap(cmd); err != nil {
			return err
		}
		if err = cmd.Run(); err != nil {
			return errors.New("development profile probe failed")
		}
		ob := launcher.Observation()
		local.Isolation.RuntimeDigest = ob.RuntimeDigest
		local.Isolation.ObservedDigest = ob.RuntimeDigest
		local.Isolation.Revision = g.Revision{Number: local.Revision, SHA256: ob.ProfileDigest}
	}
	if cfg.Harness == "fake" && !contains(local.Capabilities, "fake-no-inference") {
		local.Capabilities = append(local.Capabilities, "fake-no-inference")
		sort.Strings(local.Capabilities)
	}
	if cfg.Harness == "opencode" {
		filtered := []string{}
		for _, capability := range local.Capabilities {
			if capability != "opencode-config-isolation" {
				filtered = append(filtered, capability)
			}
		}
		local.Capabilities = filtered
		var probe probeRecord
		b, e := os.ReadFile(filepath.Join(cfg.StateDir, "probe.json"))
		if e == nil && closedjson.Decode(b, &probe, 65536, nil) == nil && probe.Passed && probe.RunnerBoot == boot.RunnerBoot {
			local.Capabilities = append(local.Capabilities, "opencode-config-isolation")
			sort.Strings(local.Capabilities)
		}
	}
	if err = local.Validate(); err != nil {
		return err
	}
	if err = runner.DurableFile(cfg.Policy, local); err != nil {
		return err
	}
	return writeJSON(out, local)
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func probeGateway(ctx context.Context, g inference.Gateway, caFile string) (bool, error) {
	info, err := os.Lstat(g.CredentialRef.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return false, errors.New("gateway credential must be a 0600 regular file")
	}
	key, err := os.ReadFile(g.CredentialRef.Path)
	if err != nil {
		return false, err
	}
	credential := strings.TrimSpace(string(key))
	if credential == "" || strings.ContainsAny(credential, "\r\n") {
		return false, errors.New("invalid gateway credential")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		b, e := os.ReadFile(caFile)
		if e != nil {
			return false, e
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(b) {
			return false, errors.New("invalid gateway CA")
		}
		tlsConfig.RootCAs = pool
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(g.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	resp, err := client.Do(req)
	if err != nil {
		return false, errors.New("gateway probe unavailable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(body) > 65536 || resp.StatusCode != 200 {
		return false, errors.New("gateway probe refused")
	}
	var envelope struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return false, errors.New("gateway models response invalid")
	}
	return true, nil
}
