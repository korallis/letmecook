package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

// NewTLS serves the identity control plane, never the execution ALPN/channel.
// endpoint is the operator-selected HTTPS origin, not a discovered peer.
func NewTLS(s *store.Store, endpoint string) (http.Handler, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != endpoint {
		return nil, i.Invalid
	}
	configured, err := s.IdentityConfigured(context.Background())
	if err != nil || !configured {
		return nil, i.Denied
	}
	return newAPI(s, u.Host, true)
}

type identityRequest struct {
	Version     string  `json:"version"`
	MessageID   string  `json:"message_id"`
	Fingerprint string  `json:"fingerprint"`
	Token       i.Token `json:"token"`
	ID          string  `json:"id"`
	Revision    int64   `json:"revision"`
	Action      string  `json:"action"`
}

// Closed flat JSON: reject duplicate/unknown/missing keys, nulls, invalid UTF-8,
// trailing values and oversized input before decoding typed fields.
func identityBody(w http.ResponseWriter, r *http.Request, fields []string) (identityRequest, error) {
	var v identityRequest
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Content-Encoding") != "" {
		return v, i.Invalid
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	if err != nil || !utf8.Valid(b) {
		return v, i.Invalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return v, i.Invalid
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"version": true, "message_id": true}
	for _, f := range fields {
		allowed[f] = true
	}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || !allowed[name] {
			return v, i.Invalid
		}
		seen[name] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return v, i.Invalid
		}
	}
	if _, err = d.Token(); err != nil || len(seen) != len(allowed) {
		return v, i.Invalid
	}
	if _, err = d.Token(); err != io.EOF {
		return v, i.Invalid
	}
	if json.Unmarshal(b, &v) != nil || v.Version != i.Version || !p.ValidID(v.MessageID) {
		return v, i.Invalid
	}
	return v, nil
}

// identityAPI checks revocation on each request, including on already-open TLS
// connections. Store mutations recheck the actor inside the write transaction.
func identityAPI(s *store.Store, w http.ResponseWriter, r *http.Request, refuse func(int, string)) bool {
	fail := func(err error) {
		code := 503
		switch {
		case errors.Is(err, i.Denied):
			code = 403
		case errors.Is(err, i.Invalid):
			code = 400
		case errors.Is(err, i.Conflict):
			code = 409
		}
		// Do not render errors: input, PEM, tokens and SQL never enter responses.
		text := map[int]string{503: "identity_unavailable", 403: "identity_denied", 400: "invalid_identity_request", 409: "identity_conflict"}[code]
		refuse(code, text)
	}
	if r.TLS == nil || !r.TLS.HandshakeComplete || r.TLS.Version < tls.VersionTLS13 || len(r.TLS.PeerCertificates) != 1 {
		fail(i.Denied)
		return true
	}
	fingerprint, err := i.Fingerprint(r.TLS.PeerCertificates[0])
	if err != nil {
		fail(err)
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/identity/")
	var actor i.Principal
	if path != "enroll" {
		actor, err = s.Authenticate(r.Context(), fingerprint)
		if err != nil {
			fail(err)
			return true
		}
		if path != "self" && actor.Role != "owner" {
			fail(i.Denied)
			return true
		}
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/identity/") {
		return false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		fail(i.Invalid)
		return true
	}
	if path == "self" && r.Method == http.MethodGet && r.ContentLength == 0 && len(r.TransferEncoding) == 0 {
		json.NewEncoder(w).Encode(actor)
		return true
	}
	if r.Method != http.MethodPost {
		refuse(405, "method_refused")
		return true
	}
	var fields []string
	switch path {
	case "enrollments":
		fields = []string{"fingerprint"}
	case "enroll":
		fields = []string{"token"}
	case "update":
		fields = []string{"id", "revision", "action", "fingerprint"}
	default:
		refuse(404, "not_found")
		return true
	}
	v, err := identityBody(w, r, fields)
	if err != nil {
		fail(err)
		return true
	}
	var result any
	switch path {
	case "enrollments":
		result, err = s.CreateEnrollment(r.Context(), fingerprint, v.MessageID, v.Fingerprint)
	case "enroll":
		result, err = s.Enroll(r.Context(), fingerprint, v.Token)
	case "update":
		result, err = s.UpdateIdentity(r.Context(), fingerprint, v.ID, v.Revision, v.Action, v.Fingerprint)
	}
	if err != nil {
		fail(err)
		return true
	}
	json.NewEncoder(w).Encode(result)
	return true
}
