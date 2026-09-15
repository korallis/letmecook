// Package httpapi serves local store reads; only fixtures expose the embedded shell.
// No authentication or execution surface.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
	a "github.com/korallis/letmecook/schemas/readapi"
	"github.com/korallis/letmecook/web"
)

// New binds Host/Origin validation to the actual already-open loopback listener.
func New(s *store.Store, addr net.Addr) (http.Handler, error) {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.Equal(net.IPv4(127, 0, 0, 1)) || tcp.Port < 1 || tcp.Port > 65535 {
		return nil, errors.New("loopback listener required")
	}
	metadata, err := s.Status(context.Background())
	if err != nil {
		return nil, err
	}
	host := tcp.String()
	slots := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		refuse := func(status int, code string) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(struct {
				Version             string   `json:"version"`
				Error               string   `json:"error"`
				Mode                string   `json:"mode"`
				MissingCapabilities []string `json:"missing_capabilities"`
			}{a.Version, code, metadata.Mode, a.MissingCapabilities()})
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(peer).IsLoopback() || r.Host != host || r.URL.IsAbs() || r.URL.Host != "" {
			refuse(403, "boundary_refused")
			return
		}
		if origins := r.Header.Values("Origin"); len(origins) > 1 || len(origins) == 1 && origins[0] != "http://"+host {
			refuse(403, "origin_refused")
			return
		}
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") || strings.EqualFold(name, "Forwarded") {
				refuse(403, "proxy_refused")
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			refuse(403, "origin_refused")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			refuse(405, "read_only")
			return
		}
		if len(r.RequestURI) > 512 || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.Header.Get("Content-Encoding") != "" {
			refuse(400, "invalid_request")
			return
		}
		if r.URL.RawPath != "" {
			refuse(404, "not_found")
			return
		}
		// Embedded read-only shell shares this origin, so browser reads need no
		// CORS or proxy exception. Same GET-only boundary checks apply above.
		if metadata.Mode == "fixture-only" && (r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/assets/")) {
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				refuse(400, "invalid_query")
				return
			}
			body, contentType, ok := web.Asset(r.URL.Path)
			if !ok && r.URL.Path == "/" {
				refuse(503, "ui_unavailable")
				return
			}
			if !ok {
				refuse(404, "not_found")
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
			w.Write(body)
			return
		}
		if r.URL.Path != "/api/v1/status" && r.URL.Path != "/api/v1/snapshot" {
			refuse(404, "not_found")
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || r.URL.ForceQuery {
			refuse(400, "invalid_query")
			return
		}
		limit := a.MaxItems
		for key, values := range q {
			if r.URL.Path == "/api/v1/status" || len(values) != 1 {
				refuse(400, "invalid_query")
				return
			}
			switch key {
			case "task_id":
				if !p.ValidID(values[0]) {
					refuse(400, "invalid_id")
					return
				}
			case "limit":
				limit, err = strconv.Atoi(values[0])
				if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 || limit > a.MaxItems {
					refuse(400, "invalid_bound")
					return
				}
			default:
				refuse(400, "invalid_query")
				return
			}
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			refuse(503, "busy")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		var value any
		if r.URL.Path == "/api/v1/status" {
			value, err = s.Status(ctx)
		} else {
			value, err = s.Snapshot(ctx, q.Get("task_id"), limit)
		}
		if errors.Is(err, sql.ErrNoRows) {
			refuse(404, "not_found")
			return
		}
		if err != nil {
			refuse(503, "store_unavailable")
			return
		}
		b, err := json.Marshal(value)
		if err != nil || len(b) > a.MaxBytes {
			refuse(503, "invalid_snapshot")
			return
		}
		w.Write(b)
	}), nil
}
