package alexa

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSignedRequests(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"echo-api.amazon.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	c := &Config{SkillID: "skill", ClientID: "client", ClientSecret: "secret", RedirectURLs: []string{"https://example.com/callback"}, roots: pool}
	fetches := 0
	c.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		fetches++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))}, nil
	})}
	for _, tc := range []struct {
		name, skill, cert string
		age               time.Duration
		tamper            bool
		good              bool
	}{
		{"valid", "skill", "https://s3.amazonaws.com/echo.api/cert.pem", 0, false, true},
		{"cached", "skill", "https://s3.amazonaws.com/echo.api/cert.pem", 0, false, true},
		{"tampered", "skill", "https://s3.amazonaws.com/echo.api/cert.pem", 0, true, false},
		{"stale", "skill", "https://s3.amazonaws.com/echo.api/cert.pem", -151 * time.Second, false, false},
		{"future", "skill", "https://s3.amazonaws.com/echo.api/cert.pem", 151 * time.Second, false, false},
		{"wrong skill", "other", "https://s3.amazonaws.com/echo.api/cert.pem", 0, false, false},
		{"ssrf", "skill", "https://localhost/echo.api/cert.pem", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Envelope{}
			e.Context.System.Application.ID = tc.skill
			e.Request.ID = "request"
			e.Request.Timestamp = now.Add(tc.age)
			body, _ := json.Marshal(e)
			hash := sha256.Sum256(body)
			sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if err != nil {
				t.Fatal(err)
			}
			if tc.tamper {
				body = append(body, ' ')
			}
			r := httptest.NewRequest("POST", "/api/alexa", bytes.NewReader(body))
			r.Header.Set("SignatureCertChainUrl", tc.cert)
			r.Header.Set("Signature-256", base64.StdEncoding.EncodeToString(sig))
			_, err = c.Read(httptest.NewRecorder(), r)
			if (err == nil) != tc.good {
				t.Fatalf("verified=%v err=%v", err == nil, err)
			}
		})
	}
	if fetches != 1 {
		t.Fatalf("certificate downloads=%d", fetches)
	}
	// A syntactically valid certificate and body are insufficient without a trusted chain.
	c.roots = x509.NewCertPool()
	e := Envelope{}
	e.Context.System.Application.ID = "skill"
	e.Request.ID = "request"
	e.Request.Timestamp = now
	body, _ := json.Marshal(e)
	hash := sha256.Sum256(body)
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	r := httptest.NewRequest("POST", "/api/alexa", bytes.NewReader(body))
	r.Header.Set("SignatureCertChainUrl", "https://s3.amazonaws.com/echo.api/cert.pem")
	r.Header.Set("Signature-256", base64.StdEncoding.EncodeToString(sig))
	if _, err := c.Read(httptest.NewRecorder(), r); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestCertificateURLs(t *testing.T) {
	for _, raw := range []string{"http://s3.amazonaws.com/echo.api/a", "https://s3.amazonaws.com.evil/echo.api/a", "https://s3.amazonaws.com:444/echo.api/a", "https://s3.amazonaws.com/echo.api/../a", "https://user@s3.amazonaws.com/echo.api/a", "https://s3.amazonaws.com/echo.api/a?x=y", "https://s3.amazonaws.com/echo.api/%2e%2e/a"} {
		if certificateURL(raw) {
			t.Errorf("accepted %s", raw)
		}
	}
	if !certificateURL("https://s3.amazonaws.com:443/echo.api/a") {
		t.Fatal("valid url rejected")
	}
	c := &Config{RedirectURLs: []string{"https://example.com/callback"}}
	for _, raw := range []string{"https://example.com/callback.evil", "https://example.com/callback#fragment", "https://example.com/callback?extra=1", strings.Repeat("x", 100)} {
		if c.AllowsRedirect(raw) {
			t.Errorf("accepted redirect %s", raw)
		}
	}
}
