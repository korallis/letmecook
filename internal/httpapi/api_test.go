package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/store"
	a "github.com/korallis/letmecook/schemas/readapi"
)

func server(t *testing.T) (*store.Store, *httptest.Server) {
	t.Helper()
	s, err := store.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	h, err := New(s, ts.Listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		if err := s.Dispose(); err != nil {
			t.Error(err)
		}
	})
	return s, ts
}
func TestRealStoreAPI(t *testing.T) {
	s, ts := server(t)
	client := ts.Client()
	client.Timeout = 3 * time.Second
	var snapshot a.Snapshot
	for _, kind := range []string{"status", "snapshot"} {
		res, err := client.Get(ts.URL + "/api/v1/" + kind)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("response %d %v", res.StatusCode, res.Header)
		}
		if kind == "snapshot" {
			if err = json.Unmarshal(body, &snapshot); err != nil {
				t.Fatal(err)
			}
			if err = snapshot.Validate(); err != nil {
				t.Fatal(err)
			}
		} else {
			var v a.Status
			if err = json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			if err = v.Validate(); err != nil || v.TaskCount != 1 || v.EventCount != 2 {
				t.Fatalf("status %+v %v", v, err)
			}
		}
	}
	taskID := snapshot.Tasks[0].TaskID
	for _, query := range []string{"?task_id=" + taskID, "?limit=1", "?task_id=" + taskID + "&limit=50"} {
		res, err := client.Get(ts.URL + "/api/v1/snapshot" + query)
		if err != nil {
			t.Fatal(err)
		}
		var v a.Snapshot
		err = json.NewDecoder(res.Body).Decode(&v)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || v.Validate() != nil || len(v.Tasks) != 1 {
			t.Fatalf("filter %s %+v %v", query, v, err)
		}
		if query == "?limit=1" && len(v.Events) != 1 {
			t.Fatal("limit ignored")
		}
	}
	cases := []struct {
		method, path, host, origin string
		body                       bool
		want                       int
	}{
		{"POST", "/api/v1/snapshot", "", "", true, 405},
		{"PUT", "/api/v1/status", "", "", false, 405},
		{"DELETE", "/api/v1/snapshot", "", "", false, 405},
		{"HEAD", "/api/v1/status", "", "", false, 405},
		{"OPTIONS", "/api/v1/status", "", "", false, 405},
		{"GET", "/api/v1/status", "evil.example", "", false, 403},
		{"GET", "/api/v1/status", "localhost", "", false, 403},
		{"GET", "/api/v1/status", "", "null", false, 403},
		{"GET", "/api/v1/status", "", "https://evil.example", false, 403},
		{"GET", "/api/v1/status", "", ts.URL + "/", false, 403},
		{"GET", "/api/v1/status", "", ts.URL, false, 200},
		{"GET", "/api/v1/status", "", "", true, 400},
		{"GET", "/api/v1/status?limit=1", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=51", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=0", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=-1", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=01", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=1e1", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=1&limit=2", "", "", false, 400},
		{"GET", "/api/v1/snapshot?task_id=../../secret", "", "", false, 400},
		{"GET", "/api/v1/snapshot?task_id=00000000-0000-4000-8000-000000000099", "", "", false, 404},
		{"GET", "/api/v1/snapshot?import=/private/state", "", "", false, 400},
		{"GET", "/api/v1/snapshot?route=http://127.0.0.1:1", "", "", false, 400},
		{"GET", "/api/v1/snapshot?limit=%zz", "", "", false, 400},
		{"GET", "/api/v1/snapshot?", "", "", false, 400},
		{"GET", "/api/v1/snapshot?x=" + strings.Repeat("a", 600), "", "", false, 400},
		{"GET", "/api/v1/snapshot/", "", "", false, 404},
		{"GET", "/api/v1/%73napshot", "", "", false, 404},
		{"GET", "/api/v1/../status", "", "", false, 404},
		{"GET", "/debug/pprof", "", "", false, 404},
		{"GET", "/api/v1/enroll", "", "", false, 404},
		{"GET", "/api/v1/grant", "", "", false, 404},
		{"GET", "/api/v1/accept", "", "", false, 404},
		{"GET", "/api/v1/publish", "", "", false, 404},
		{"GET", "/", "evil.example", "", false, 403},
		{"GET", "/", "", "https://evil.example", false, 403},
		{"GET", "/?import=/private/state", "", "", false, 400},
		{"GET", "/?", "", "", false, 400},
		{"POST", "/", "", "", true, 405},
		{"GET", "/index.html", "", "", false, 404},
		{"GET", "/assets/", "", "", false, 404},
		{"GET", "/assets/../api/v1/status", "", "", false, 404},
		{"GET", "/assets/%2e%2e/index.html", "", "", false, 404},
		{"GET", "/assets/missing.js", "", "", false, 404},
		{"GET", "/assets/missing.js?enable=actions", "", "", false, 400},
		{"GET", "/dist/index.html", "", "", false, 404},
		{"GET", "/web/dist/index.html", "", "", false, 404},
		{"GET", "/ui/enable", "", "", false, 404},
	}
	for _, c := range cases {
		t.Run(c.method+c.path+c.origin+c.host, func(t *testing.T) {
			var body io.Reader
			if c.body {
				body = strings.NewReader(`{"command":"touch sentinel","url":"http://127.0.0.1:1"}`)
			}
			req, err := http.NewRequest(c.method, ts.URL+c.path, body)
			if err != nil {
				t.Fatal(err)
			}
			if c.host != "" {
				req.Host = c.host
			}
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != c.want || res.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("got %d want %d", res.StatusCode, c.want)
			}
		})
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "Sec-Fetch-Site", "Origin"} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/status", nil)
		req.Header.Add(header, "cross-site")
		if header == "Origin" {
			req.Header.Add(header, ts.URL)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatal("header accepted", header)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := client.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal("unavailable store reported healthy")
	}
}

// The embedded shell is served from the same loopback origin under the same
// GET-only boundary; every asset is a hashed build output or refused.
func TestEmbeddedShell(t *testing.T) {
	_, ts := server(t)
	client := ts.Client()
	res, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode == 503 {
		if !strings.Contains(string(body), `"error":"ui_unavailable"`) || res.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unbuilt shell response %d %s", res.StatusCode, body)
		}
		t.Skip("web/dist not built; run npm --prefix web run build for the served-shell assertions")
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.HasPrefix(string(body), "<!doctype html>") || !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("shell %d %v %.80s", res.StatusCode, res.Header, body)
	}
	for _, m := range regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(string(body), -1) {
		res, err := client.Get(ts.URL + m[1])
		if err != nil {
			t.Fatal(err)
		}
		asset, _ := io.ReadAll(res.Body)
		res.Body.Close()
		want := "text/javascript; charset=utf-8"
		if strings.HasSuffix(m[1], ".css") {
			want = "text/css; charset=utf-8"
		}
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != want || len(asset) == 0 || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("asset %s %d %v", m[1], res.StatusCode, res.Header)
		}
		if strings.HasSuffix(m[1], ".js") {
			for _, forbidden := range []string{"EventSource", "serviceWorker", "WebSocket", "localStorage", "sessionStorage", "indexedDB", "document.cookie", "method:\"POST\"", "/api/v1/enroll", "/api/v1/grant", "/api/v1/accept"} {
				if strings.Contains(string(asset), forbidden) {
					t.Fatalf("shell bundle contains %q", forbidden)
				}
			}
		}
	}
}

func TestRemoteBindingRefused(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "192.0.2.1", "::", "::1"} {
		if _, err := New(nil, &net.TCPAddr{IP: net.ParseIP(ip), Port: 8000}); err == nil {
			t.Fatal("remote/noncanonical binding accepted", ip)
		}
	}
}
