package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/korallis/letmecook/internal/closedjson"
)

func mockGateway(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("mock-gateway (test-only)", flag.ContinueOnError)
	listen := f.String("listen", "127.0.0.1:0", "loopback only")
	keyFile := f.String("key-file", "", "0600 mock bearer credential")
	models := f.String("models", "gpt-6-astra", "comma separated synthetic model IDs")
	hang := f.String("hang-after", "", "test-only: respond to n model requests, then hold responses until shutdown")
	if err := f.Parse(args); err != nil {
		return err
	}
	var hangAfter uint64
	if *hang != "" {
		var err error
		hangAfter, err = strconv.ParseUint(*hang, 10, 63)
		if err != nil {
			return errors.New("hang-after requires a nonnegative integer")
		}
	}
	var modelRequests atomic.Uint64
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("mock gateway requires loopback IP")
	}
	info, err := os.Lstat(*keyFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return errors.New("mock key must be a 0600 regular file")
	}
	key, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	secret := strings.TrimSpace(string(key))
	if secret == "" {
		return errors.New("empty mock credential")
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Gaffer test-only mock gateway"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP(host)}, DNSNames: []string{"localhost"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		return err
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caPath := filepath.Clean(*keyFile) + ".ca.crt"
	if err = os.WriteFile(caPath, pemCert, 0600); err != nil {
		return err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+secret)) != 1 {
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			data := []any{}
			for _, m := range strings.Split(*models, ",") {
				data = append(data, map[string]string{"id": m, "object": "model", "owned_by": "test-only"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v1/chat/completions" {
			body, err := io.ReadAll(io.LimitReader(r.Body, 65537))
			var req struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
				Stream bool `json:"stream,omitempty"`
			}
			if err != nil || closedjson.Decode(body, &req, 65536, nil) != nil || req.Model == "" || len(req.Messages) == 0 || req.Stream {
				http.Error(w, "malformed", 400)
				return
			}
			if *hang != "" && modelRequests.Add(1) > hangAfter {
				// The normal five-second write timeout must not turn this fault
				// into a response. Only mock shutdown releases accepted requests.
				if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
					return
				}
				<-ctx.Done()
				panic(http.ErrAbortHandler) // never synthesize a successful empty response
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "test-only", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "synthetic mock response"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1}})
			return
		}
		http.NotFound(w, r)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); server.Close() }()
	if err = writeJSON(out, map[string]any{"url": "https://" + ln.Addr().String(), "ca_file": caPath, "qualification": "test-only", "supported": false}); err != nil {
		return err
	}
	err = server.Serve(tls.NewListener(ln, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
