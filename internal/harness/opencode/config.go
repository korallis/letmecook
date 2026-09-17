package opencode

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/harness"
)

func validLabel(s string, max int) bool {
	return s != "" && len(s) <= max && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0
}
func validateRun(req harness.RunRequest) (string, error) {
	if req.Launcher == nil || req.Brief == "" || len(req.Brief) > 65536 || len(req.Boundary.Token) < 32 || !validLabel(req.Boundary.Token, 1024) || len(req.Boundary.Models) == 0 {
		return "", ErrRun
	}
	if req.Route.Harness != "" && req.Route.Harness != "opencode" {
		return "", ErrRun
	}
	if req.Route.Protocol != "" && req.Route.Protocol != req.Boundary.Protocol {
		return "", ErrRun
	}
	if _, err := boundaryURL(req.Boundary.URL); err != nil {
		return "", err
	}
	if !slices.Contains([]string{"chat_completions", "responses", "messages"}, req.Boundary.Protocol) {
		return "", ErrRun
	}
	model := req.Boundary.Models[0]
	if len(req.Route.Targets) > 0 {
		model = req.Route.Targets[0].Model
	}
	if !validLabel(model, 256) || !slices.Contains(req.Boundary.Models, model) {
		return "", ErrRun
	}
	// Repository content cannot inject settings, providers, commands or plugins.
	if err := ValidateSettings(req.Settings, model); err != nil {
		return "", err
	}
	if req.Limits.MaxStdoutBytes <= 0 || req.Limits.MaxStdoutBytes > 64<<20 {
		return "", ErrRun
	}
	return model, nil
}
func boundaryURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/" && u.Path != "/v1") {
		return "", ErrRun
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", ErrRun
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", ErrRun
	}
	u.Path = "/v1"
	return u.String(), nil
}
func prepareWorkspace(w harness.Workspace, probe bool) error {
	roots := []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir}
	resolved := make([]string, len(roots))
	for i, path := range roots {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ErrRun
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && i > 0 {
			err = os.MkdirAll(path, 0700)
			if err == nil {
				info, err = os.Lstat(path)
			}
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrRun
		}
		resolved[i], err = filepath.EvalSymlinks(path)
		if err != nil {
			return ErrRun
		}
		if i > 0 {
			if info.Mode().Perm()&0077 != 0 {
				return ErrRun
			}
			entries, err := os.ReadDir(path)
			if err != nil || ((probe || i == 1) && len(entries) > 0) {
				return ErrRun
			}
		}
	}
	for i, root := range resolved {
		for j, other := range resolved {
			if i != j && (root == other || strings.HasPrefix(root, other+string(filepath.Separator))) {
				return ErrRun
			}
		}
	}
	return nil
}
func writeConfig(w harness.Workspace, b harness.BoundaryHandle, model string) (string, error) {
	base, err := boundaryURL(b.URL)
	if err != nil {
		return "", err
	}
	npm := "@ai-sdk/openai-compatible"
	switch b.Protocol {
	case "responses":
		npm = "@ai-sdk/openai"
	case "messages":
		npm = "@ai-sdk/anthropic"
	case "chat_completions":
	default:
		return "", ErrRun
	}
	config := map[string]any{"$schema": "https://opencode.ai/config.json", "provider": map[string]any{"gaffer": map[string]any{"npm": npm, "name": "Gaffer boundary", "options": map[string]any{"baseURL": base, "apiKey": "{env:GAFFER_ATTEMPT_TOKEN}"}, "models": map[string]any{model: map[string]any{}}}}, "model": "gaffer/" + model, "permission": map[string]string{"*": "allow"}, "share": "disabled", "autoupdate": false}
	raw, err := json.Marshal(config)
	if err != nil {
		return "", ErrRun
	}
	path := filepath.Join(w.RuntimeDir, "opencode.json")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", ErrRun
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return "", ErrRun
	}
	d, err := os.Open(w.RuntimeDir)
	if err != nil {
		return "", ErrRun
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return "", ErrRun
	}
	return path, nil
}
func environment(w harness.Workspace, config, token string) []string {
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + w.PrivateHome, "XDG_CONFIG_HOME=" + filepath.Join(w.PrivateHome, ".config"), "XDG_DATA_HOME=" + filepath.Join(w.PrivateHome, ".local", "share"), "XDG_CACHE_HOME=" + filepath.Join(w.PrivateHome, ".cache"), "TMPDIR=" + w.TempDir, "OPENCODE_CONFIG=" + config, "GAFFER_ATTEMPT_TOKEN=" + token, "TERM=dumb", "NO_COLOR=1", "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_MODELS_FETCH=1"}
	return append(env, configControls...)
}
func argv(brief, model, root string) []string {
	return []string{"run", brief, "--format", "json", "-m", "gaffer/" + model, "--dir", root, "--auto", "--pure", "--print-logs"}
}

// ValidateSettings checks owner-approved direct settings against an already
// selected route model. It never chooses a model or expands the route scope.
func ValidateSettings(raw json.RawMessage, model string) error {
	var settings struct {
		Model   string `json:"model"`
		Variant string `json:"variant,omitempty"`
	}
	if closedjson.Decode(raw, &settings, 49152, nil) != nil || !validLabel(settings.Model, 256) {
		return fmt.Errorf("%w: malformed_settings", ErrRun)
	}
	if settings.Model != model {
		return fmt.Errorf("%w: settings_model_mismatch", ErrRun)
	}
	if settings.Variant != "" {
		return fmt.Errorf("%w: unsupported_variant", ErrRun)
	}
	return nil
}
