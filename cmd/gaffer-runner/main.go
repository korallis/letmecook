// gaffer-runner is a trusted development supervisor, never a supported runtime.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/execclient"
	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/harness/fake"
	"github.com/korallis/letmecook/internal/harness/opencode"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/runner"
	sc "github.com/korallis/letmecook/internal/scheduler"
)

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func main() {
	ctx := context.Background()
	// Synthetic jobs retain OS default signal behavior except modes that explicitly
	// ignore TERM. Installing the supervisor's NotifyContext here swallows TERM.
	if len(os.Args) < 2 || os.Args[1] != "fake-job" {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: gaffer-runner serve|facts|fake-job|guardian")
	}
	switch args[0] {
	case "guardian":
		fd := os.NewFile(3, "guardian-control")
		if fd == nil {
			return errors.New("guardian control missing")
		}
		defer fd.Close()
		return runner.Guardian(ctx, os.Stdin, fd, os.Stdout, os.Stderr)
	case "fake-job":
		f := flag.NewFlagSet("fake-job", flag.ContinueOnError)
		spec := f.String("spec", "", "private synthetic job spec")
		child := f.Bool("child", false, "synthetic child")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *child {
			fake.Child()
			return nil
		}
		code := fake.Job(*spec, out)
		if code != 0 {
			return fmt.Errorf("fake job exit %d", code)
		}
		return nil
	case "serve":
		c, err := parseConfig(args[1:])
		if err != nil {
			return err
		}
		return serve(ctx, c, out)
	case "facts":
		return facts(ctx, args[1:], out)
	case "mock-gateway":
		return mockGateway(ctx, args[1:], out)
	default:
		return errors.New("unknown runner subcommand")
	}
}

type config struct {
	StateDir, Daemon, Fingerprint, Cert, Key, RepositoryRoot, Isolation, Harness, GatewayConfig, OpenCodeBin, Policy, RepositoryProfile string
	SpoolLimit                                                                                                                          int64
	DaemonStateDir                                                                                                                      string `json:"daemon_state_dir,omitempty"`
}

func parseConfig(args []string) (config, error) {
	var c config
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	f.StringVar(&c.StateDir, "state-dir", "", "private supervisor state")
	f.StringVar(&c.DaemonStateDir, "daemon-state-dir", "", "deny colocated daemon private state to jobs")
	f.StringVar(&c.Daemon, "daemon", "", "execution HTTPS endpoint")
	f.StringVar(&c.Fingerprint, "daemon-fingerprint", "", "pinned daemon SHA256")
	f.StringVar(&c.Cert, "cert", "", "runner certificate")
	f.StringVar(&c.Key, "key", "", "runner private key")
	f.StringVar(&c.RepositoryRoot, "repository-root", "", "operator-provisioned 0700 checkout root")
	f.StringVar(&c.Isolation, "isolation-profile", "unqualified", "explicit development profile")
	f.StringVar(&c.Harness, "harness", "fake", "adapter")
	f.StringVar(&c.GatewayConfig, "gateway-config", "", "supervisor-only gateway configuration")
	f.StringVar(&c.OpenCodeBin, "opencode-bin", "", "pinned adapter binary")
	f.StringVar(&c.Policy, "policy", "", "trusted local eligibility file")
	f.StringVar(&c.RepositoryProfile, "repository-profile", "", "trusted local repository profile")
	f.Int64Var(&c.SpoolLimit, "spool-bytes", 4<<20, "durable stream limit")
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if f.NArg() != 0 || !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.RepositoryRoot) || c.DaemonStateDir != "" && !filepath.IsAbs(c.DaemonStateDir) {
		return c, errors.New("absolute state-dir and repository-root required")
	}
	if c.Policy == "" {
		c.Policy = filepath.Join(c.StateDir, "policy.json")
	}
	if c.RepositoryProfile == "" {
		c.RepositoryProfile = filepath.Join(c.StateDir, "repository.json")
	}
	return c, nil
}

var harnessFactories = map[string]func(config) (h.Harness, error){
	"fake":     func(config) (h.Harness, error) { exe, err := os.Executable(); return fake.New(exe), err },
	"opencode": func(c config) (h.Harness, error) { return opencode.New(c.OpenCodeBin) },
}

func harnessFor(c config) (h.Harness, error) {
	factory := harnessFactories[c.Harness]
	if factory == nil {
		return nil, errors.New("harness unavailable in this build")
	}
	return factory(c)
}
func profileFor(c config) (isolation.Profile, error) {
	if c.Isolation == "unqualified" {
		return isolation.Unqualified{}, nil
	}
	if c.Isolation != isolation.DevelopmentProfileID {
		return nil, isolation.ErrExecutionUnqualified
	}
	config := isolation.SandboxConfig{RepositoryRoot: c.RepositoryRoot, RunnerStateDir: c.StateDir, SecretPaths: []string{c.Key, c.GatewayConfig, c.DaemonStateDir}}
	if c.GatewayConfig != "" {
		gateway, err := inference.LoadGatewayConfig(c.GatewayConfig)
		if err != nil {
			return nil, err
		}
		config.CredentialPath = gateway.CredentialRef.Path
	}
	return isolation.DevelopmentProfile(config)
}
func loadPolicy(path string) (sc.Eligibility, error) {
	var v sc.Eligibility
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return v, errors.New("trusted local policy must be a non-writable regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if err = closedjson.Decode(b, &v, 65536, map[string]bool{"provider_cost_micros": true}); err != nil {
		return v, err
	}
	return v, v.Validate()
}

type bootRecord struct {
	RunnerBoot string    `json:"runner_boot"`
	PID        int       `json:"pid"`
	Started    time.Time `json:"started"`
}

func openClient(c config) (*execclient.Client, error) {
	cert, err := tls.LoadX509KeyPair(c.Cert, c.Key)
	if err != nil {
		return nil, err
	}
	return execclient.New(execclient.Options{Endpoint: c.Daemon, Fingerprint: c.Fingerprint, Certificate: cert})
}
func writeJSON(out io.Writer, v any) error { return json.NewEncoder(out).Encode(v) }
