// Package httpapi serves bounded store reads and the provisional mTLS identity API.
// Only fixtures expose the embedded shell; execution uses a separate listener.
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
	if metadata.Mode != "fixture-only" {
		configured, err := s.IdentityConfigured(context.Background())
		if err != nil {
			return nil, err
		}
		if configured {
			return nil, errors.New("authenticated HTTPS required")
		}
	}
	return newAPI(s, tcp.String(), false)
}

// legacyAPI retains the identity and read response/query contract unchanged.
// guard and the shared control pool run before this handler.
func legacyAPI(s *store.Store, metadata a.Status, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		refuse := legacyRefuse(metadata, secure, w, r)
		if secure && identityAPI(s, w, r, refuse) {
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			refuse(405, "read_only")
			return
		}
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			refuse(400, "invalid_request")
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
	})
}
