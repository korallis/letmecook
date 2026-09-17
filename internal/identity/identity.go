// Package identity owns pinned mTLS identities, not runner eligibility or execution.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"slices"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

const Version = "identity-provisional-v1"

var (
	Invalid     = errors.New("invalid_identity_request")
	Denied      = errors.New("identity_denied")
	Conflict    = errors.New("identity_conflict")
	Unavailable = errors.New("identity_unavailable")
)

type Principal struct {
	Version  string `json:"version"`
	ID       string `json:"id"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
	Revoked  bool   `json:"revoked"`
	Revision int64  `json:"revision"`
}

// Token is revealed only in its intentional enrollment response, never formatting.
type Token string

func (Token) String() string   { return "[REDACTED]" }
func (Token) GoString() string { return "[REDACTED]" }
func NewToken() Token {
	var b [32]byte
	rand.Read(b[:])
	return Token(hex.EncodeToString(b[:]))
}
func ValidFingerprint(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func TokenHash(token Token) (string, error) {
	if !ValidFingerprint(string(token)) {
		return "", Denied
	}
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:]), nil
}

// Fingerprint validates a client leaf and pins its exact DER bytes. The TLS
// CertificateVerify handshake proves possession; neither subject nor SAN grants a role.
func Fingerprint(cert *x509.Certificate) (string, error) {
	if cert == nil || cert.IsCA || cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return "", Denied
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return "", Denied
	}
	h := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(h[:]), nil
}

// ReadCertificate reads public client material for offline owner bootstrap/recovery.
func ReadCertificate(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", Invalid
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return "", Invalid
	}
	block, rest := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return "", Invalid
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", Invalid
	}
	return Fingerprint(cert)
}

// ServerTLS accepts unknown client certificates ONLY so a pinned one-use invite
// can enroll them. Every HTTP request still requires current store authorization.
func ServerTLS(certFile, keyFile string) (*tls.Config, error) {
	st, err := os.Lstat(keyFile)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return nil, Invalid
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, Invalid
	}
	if cert.Leaf == nil || !slices.Contains(cert.Leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) || time.Now().Before(cert.Leaf.NotBefore) || !time.Now().Before(cert.Leaf.NotAfter) {
		return nil, Invalid
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, Invalid
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
		ClientAuth: tls.RequireAnyClientCert, SessionTicketsDisabled: true,
		NextProtos: []string{"http/1.1"},
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) != 1 {
				return Denied
			}
			_, err := Fingerprint(cs.PeerCertificates[0])
			return err
		},
	}, nil
}

// ExecutionTLS uses the same client pins and certificate rules as ServerTLS,
// but advertises only the provisional execution-channel ALPN.
func ExecutionTLS(certFile, keyFile string) (*tls.Config, error) {
	cfg, err := ServerTLS(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	cfg.NextProtos = []string{p.FencedVersion}
	return cfg, nil
}
