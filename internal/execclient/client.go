// Package execclient carries the runner-only execution channel over pinned mTLS.
package execclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	w "github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
)

type Options struct {
	Endpoint, Fingerprint string
	Certificate           tls.Certificate
	Attempts              int
	RetryDelay            time.Duration
}
type Client struct {
	http              *http.Client
	base, fingerprint string
	attempts          int
	delay             time.Duration
	mu                sync.RWMutex
	session           string
}
type Error struct {
	Status       int
	Code, Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("execution %d %s: %s", e.Status, e.Code, e.Detail) }
func IsFence(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == "stale_generation" || e.Code == "boot_mismatch" || e.Code == "session_stale" || e.Code == "revoked_or_expired" || e.Code == "runner_disabled")
}
func New(o Options) (*Client, error) {
	u, err := url.Parse(o.Endpoint)
	pin, pe := hex.DecodeString(o.Fingerprint)
	if err != nil || pe != nil || len(pin) != 32 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || len(o.Certificate.Certificate) == 0 {
		return nil, errors.New("invalid execution client configuration")
	}
	// The explicit leaf fingerprint, not an ambient CA/proxy, is the trust anchor.
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{p.FencedVersion}, Certificates: []tls.Certificate{o.Certificate}, InsecureSkipVerify: true, VerifyConnection: func(s tls.ConnectionState) error {
		if s.NegotiatedProtocol != p.FencedVersion || len(s.PeerCertificates) != 1 {
			return errors.New("alpn_refused")
		}
		sum := sha256.Sum256(s.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
			return errors.New("daemon_fingerprint_mismatch")
		}
		now := time.Now()
		if now.Before(s.PeerCertificates[0].NotBefore) || !now.Before(s.PeerCertificates[0].NotAfter) {
			return errors.New("daemon_certificate_expired")
		}
		return nil
	}}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: false, TLSClientConfig: cfg, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, MaxIdleConnsPerHost: 8, ResponseHeaderTimeout: 130 * time.Second, IdleConnTimeout: 60 * time.Second}
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: cfg}
		return d.DialContext(ctx, network, addr)
	}
	if o.Attempts == 0 {
		o.Attempts = 3
	}
	if o.Attempts < 1 || o.Attempts > 10 {
		return nil, errors.New("invalid retry count")
	}
	if o.RetryDelay == 0 {
		o.RetryDelay = 100 * time.Millisecond
	}
	return &Client{http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: strings.TrimRight(o.Endpoint, "/"), fingerprint: o.Fingerprint, attempts: o.Attempts, delay: o.RetryDelay}, nil
}
func (c *Client) Close()            { c.http.CloseIdleConnections() }
func (c *Client) SessionID() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.session }
func (c *Client) call(ctx context.Context, method, path string, body []byte, out any, raw bool) error {
	var last error
	for attempt := 0; attempt < c.attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+"/x/v1"+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if path != "/session" {
			s := c.SessionID()
			if s == "" {
				return &Error{409, "session_stale", "hello required"}
			}
			req.Header.Set("X-Gaffer-Session", s)
		}
		if raw {
			req.Header.Set("Content-Type", "application/octet-stream")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err == nil {
			b, re := io.ReadAll(io.LimitReader(resp.Body, w.MaxBytes+1))
			resp.Body.Close()
			if re == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out == nil {
					return nil
				}
				if m, ok := out.(*p.Message); ok {
					var e error
					*m, e = p.Decode(b)
					return e
				}
				return w.Decode(b, out)
			}
			if re != nil {
				last = re
			} else {
				var e w.ErrorBody
				if w.Decode(b, &e) != nil || e.Version != w.Version {
					return errors.New("invalid execution error response")
				}
				last = &Error{resp.StatusCode, e.Error, e.Detail}
				if resp.StatusCode != 503 && resp.StatusCode != 502 && resp.StatusCode != 504 {
					return last
				}
			}
		} else {
			last = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt+1 < c.attempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.delay):
			}
		}
	}
	return last
}
func (c *Client) json(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	var err error
	if in != nil {
		body, err = w.Encode(in)
		if err != nil {
			return err
		}
	}
	return c.call(ctx, method, path, body, out, false)
}
func (c *Client) Hello(ctx context.Context, in w.Hello) (w.Session, error) {
	var out w.Session
	err := c.json(ctx, "POST", "/session", in, &out)
	if err == nil {
		if !p.ValidID(out.SessionID) || !p.ValidID(out.Generation) || !p.ValidID(out.DaemonBoot) || !p.ValidID(out.RunnerID) || out.DaemonFingerprint != "" && out.DaemonFingerprint != c.fingerprint || out.Mode != "normal" && out.Mode != "recovery_only" || out.DriftMS <= 0 || out.TerminationMS <= 0 || out.TerminationMS > 5000 || out.LeaseValidityMS <= out.DriftMS+out.TerminationMS || out.RenewEveryMS <= 0 || out.RenewEveryMS >= out.LeaseValidityMS-out.DriftMS-out.TerminationMS {
			return out, errors.New("invalid execution session")
		}
		c.mu.Lock()
		c.session = out.SessionID
		c.mu.Unlock()
	}
	return out, err
}
func (c *Client) State(ctx context.Context, id string) (w.State, error) {
	var out w.State
	err := c.json(ctx, "GET", "/state?dispatch_id="+url.QueryEscape(id), nil, &out)
	return out, err
}
func (c *Client) Inbox(ctx context.Context, waitMS int) (w.Inbox, error) {
	var out w.Inbox
	if waitMS < 0 || waitMS > 25000 {
		return out, errors.New("invalid inbox wait")
	}
	err := c.json(ctx, "GET", "/inbox?wait_ms="+strconv.Itoa(waitMS), nil, &out)
	return out, err
}

type MessageReply struct {
	Outcome  string     `json:"outcome"`
	Message  *p.Message `json:"message,omitempty"`
	Released bool       `json:"released,omitempty"`
}

func (c *Client) Message(ctx context.Context, in w.MessageEnvelope) (MessageReply, error) {
	var out MessageReply
	err := c.json(ctx, "POST", "/messages", in, &out)
	if err == nil && out.Message != nil {
		b, _ := json.Marshal(out.Message)
		var m p.Message
		m, err = p.Decode(b)
		out.Message = &m
	}
	return out, err
}
func (c *Client) Lease(ctx context.Context, in w.LeaseEnvelope) (p.Message, error) {
	var out p.Message
	err := c.json(ctx, "POST", "/lease", in, &out)
	if err == nil {
		b, _ := json.Marshal(out)
		out, err = p.Decode(b)
	}
	return out, err
}
func (c *Client) Stream(ctx context.Context, id string, in w.StreamBatch) (w.StreamAck, error) {
	var out w.StreamAck
	err := c.json(ctx, "POST", "/streams/"+url.PathEscape(id), in, &out)
	return out, err
}
func (c *Client) BeginUpload(ctx context.Context, id string, in w.UploadBegin) (w.UploadSession, error) {
	var out w.UploadSession
	err := c.json(ctx, "POST", "/attempts/"+url.PathEscape(id)+"/uploads", in, &out)
	return out, err
}

type BlobReply struct {
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Duplicate bool   `json:"duplicate"`
}

func (c *Client) PutBlob(ctx context.Context, upload, digest string, body []byte) (BlobReply, error) {
	var out BlobReply
	if len(body) > 64<<20 {
		return out, errors.New("blob exceeds limit")
	}
	err := c.call(ctx, "PUT", "/uploads/"+url.PathEscape(upload)+"/blobs/"+url.PathEscape(digest), body, &out, true)
	return out, err
}

type Intent struct {
	Version   string `json:"version"`
	MessageID string `json:"message_id"`
}

func (c *Client) Commit(ctx context.Context, upload, messageID string) (w.CommitReply, error) {
	var out w.CommitReply
	err := c.json(ctx, "POST", "/uploads/"+url.PathEscape(upload)+"/commit", Intent{w.Version, messageID}, &out)
	return out, err
}

type FinalizeReply struct {
	Outcome  string `json:"outcome"`
	Released bool   `json:"released"`
}

func (c *Client) Finalize(ctx context.Context, id string, in w.Completion) (FinalizeReply, error) {
	var out FinalizeReply
	err := c.json(ctx, "POST", "/attempts/"+url.PathEscape(id)+"/finalize", in, &out)
	return out, err
}
func (c *Client) Usage(ctx context.Context, in w.Usage) error {
	var reply struct {
		Outcome string `json:"outcome"`
	}
	return c.json(ctx, "POST", "/usage", in, &reply)
}
