package inference

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/korallis/letmecook/internal/closedjson"
)

const GatewayConfigVersion = "gateway-config-v1"
const MaxGatewayConfigBytes = 16 * 1024

var ErrGatewayConfig = errors.New("invalid_gateway_config")

type gatewayConfig struct {
	Version       string        `json:"version"`
	ID            string        `json:"gateway_id"`
	BaseURL       string        `json:"base_url"`
	CABundle      string        `json:"ca_bundle,omitempty"`
	CredentialRef CredentialRef `json:"credential_ref"`
	Protocols     []string      `json:"protocols"`
	Models        []string      `json:"models"`
}

// LoadGatewayConfig validates explicit operator configuration without reading the
// credential or contacting the gateway. Credential ownership/mode and read-once
// semantics belong to the supervisor when it starts the boundary.
func LoadGatewayConfig(path string) (Gateway, error) {
	f, err := os.Open(path)
	if err != nil {
		return Gateway{}, ErrGatewayConfig
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxGatewayConfigBytes+1))
	if err != nil {
		return Gateway{}, ErrGatewayConfig
	}
	var c gatewayConfig
	if closedjson.Decode(b, &c, MaxGatewayConfigBytes, nil) != nil || c.Version != GatewayConfigVersion || !label(c.ID, 64) {
		return Gateway{}, ErrGatewayConfig
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != c.BaseURL {
		return Gateway{}, ErrGatewayConfig
	}
	if c.CredentialRef.Kind != "file" || !filepath.IsAbs(c.CredentialRef.Path) || filepath.Clean(c.CredentialRef.Path) != c.CredentialRef.Path || strings.ContainsRune(c.CredentialRef.Path, 0) {
		return Gateway{}, ErrGatewayConfig
	}
	if c.CABundle != "" && (!filepath.IsAbs(c.CABundle) || filepath.Clean(c.CABundle) != c.CABundle || strings.ContainsRune(c.CABundle, 0)) {
		return Gateway{}, ErrGatewayConfig
	}
	if !list(c.Protocols, 3) || !list(c.Models, 256) {
		return Gateway{}, ErrGatewayConfig
	}
	for _, protocol := range c.Protocols {
		if protocolPaths[protocol] == "" {
			return Gateway{}, ErrGatewayConfig
		}
	}
	canonical, err := json.Marshal(c)
	if err != nil {
		return Gateway{}, ErrGatewayConfig
	}
	sum := sha256.Sum256(canonical)
	return Gateway{ID: c.ID, BaseURL: c.BaseURL, CABundle: c.CABundle, CredentialRef: c.CredentialRef, Protocols: c.Protocols, Models: c.Models, ProfileDigest: hex.EncodeToString(sum[:])}, nil
}
func label(s string, max int) bool {
	return s != "" && len(s) <= max && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0
}
func list(values []string, max int) bool {
	if len(values) == 0 || len(values) > max {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !label(v, 256) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
