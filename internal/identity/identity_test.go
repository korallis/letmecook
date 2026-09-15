package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCertificateAndTokenValidation(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"valid", "expired", "future", "ca", "wrong-usage", "no-usage", "no-signature"} {
		t.Run(kind, func(t *testing.T) {
			c := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
			switch kind {
			case "expired":
				c.NotAfter = time.Now().Add(-time.Second)
			case "future":
				c.NotBefore = time.Now().Add(time.Minute)
			case "ca":
				c.IsCA = true
			case "wrong-usage":
				c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			case "no-usage":
				c.ExtKeyUsage = nil
			case "no-signature":
				c.KeyUsage = x509.KeyUsageKeyEncipherment
			}
			der, err := x509.CreateCertificate(rand.Reader, c, c, pub, key)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "client.crt")
			if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			pin, err := ReadCertificate(file)
			if kind == "valid" {
				if err != nil || !ValidFingerprint(pin) {
					t.Fatal("valid client refused")
				}
			} else if err != Denied {
				t.Fatal("invalid client admitted", err)
			}
		})
	}
	if _, err := Fingerprint(nil); err != Denied {
		t.Fatal("nil certificate")
	}
	first, second := NewToken(), NewToken()
	hash, err := TokenHash(first)
	if err != nil || first == second || hash == string(first) || !ValidFingerprint(hash) {
		t.Fatal("invalid secret/hash")
	}
	if _, err := TokenHash("short-secret"); err != Denied {
		t.Fatal("invalid secret admitted")
	}
	file := filepath.Join(t.TempDir(), "invalid.crt")
	if err := os.WriteFile(file, make([]byte, 16385), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCertificate(file); err != Invalid {
		t.Fatal("oversized certificate")
	}
}
