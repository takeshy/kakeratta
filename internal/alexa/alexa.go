// Package alexa implements the signed custom-skill HTTPS boundary.
package alexa

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

type Identity struct {
	Application struct {
		ID string `json:"applicationId"`
	} `json:"application"`
	User struct {
		AccessToken string `json:"accessToken"`
	} `json:"user"`
}

type Envelope struct {
	Context struct {
		System Identity `json:"System"`
	} `json:"context"`
	Session struct {
		Identity
		Attributes map[string]string `json:"attributes"`
	} `json:"session"`
	Request struct {
		Type      string    `json:"type"`
		ID        string    `json:"requestId"`
		Timestamp time.Time `json:"timestamp"`
		Locale    string    `json:"locale"`
		Intent    struct {
			Name  string `json:"name"`
			Slots map[string]struct {
				Value string `json:"value"`
			} `json:"slots"`
		} `json:"intent"`
	} `json:"request"`
}

func (e Envelope) Token() string { return e.Context.System.User.AccessToken }

type Config struct {
	SkillID, ClientID, ClientSecret string
	RedirectURLs                    []string
	mu                              sync.Mutex
	certs                           map[string][]*x509.Certificate
	client                          *http.Client
	roots                           *x509.CertPool
}

func (c *Config) Enabled() bool {
	return c != nil && c.SkillID != "" && c.ClientID != "" && c.ClientSecret != "" && len(c.RedirectURLs) > 0
}

func (c *Config) AllowsRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	for _, allowed := range c.RedirectURLs {
		if raw == allowed {
			return true
		}
	}
	return false
}

func certificateURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() == "s3.amazonaws.com" &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		strings.HasPrefix(path.Clean(u.Path), "/echo.api/")
}

// Read verifies the exact body before trusting its skill, identity or intent.
// Only SHA-256 signatures are accepted; certificates are verified on every use.
func (c *Config) Read(w http.ResponseWriter, r *http.Request) (Envelope, error) {
	var e Envelope
	bad := errors.New("invalid Alexa request")
	if !c.Enabled() || r.Method != http.MethodPost {
		return e, bad
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || json.Unmarshal(body, &e) != nil {
		return e, bad
	}
	if e.Context.System.Application.ID != c.SkillID || (e.Session.Application.ID != "" && e.Session.Application.ID != c.SkillID) ||
		e.Request.ID == "" || time.Since(e.Request.Timestamp) > 150*time.Second || time.Until(e.Request.Timestamp) > 150*time.Second {
		return e, bad
	}
	raw := r.Header.Get("SignatureCertChainUrl")
	if !certificateURL(raw) {
		return e, bad
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("Signature-256"))
	if err != nil || len(sig) == 0 {
		return e, bad
	}
	c.mu.Lock()
	certs := c.certs[raw]
	c.mu.Unlock()
	if len(certs) == 0 {
		client := c.client
		if client == nil {
			client = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
		if err != nil {
			return e, bad
		}
		res, err := client.Do(req)
		if err != nil {
			return e, bad
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || len(data) > 64<<10 {
			return e, bad
		}
		for len(data) > 0 {
			block, rest := pem.Decode(data)
			if block == nil {
				break
			}
			data = rest
			if block.Type != "CERTIFICATE" {
				return e, bad
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return e, bad
			}
			certs = append(certs, cert)
		}
	}
	if len(certs) == 0 {
		return e, bad
	}
	exactName := false
	for _, name := range certs[0].DNSNames {
		if name == "echo-api.amazon.com" {
			exactName = true
		}
	}
	if !exactName {
		return e, bad
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{DNSName: "echo-api.amazon.com", Roots: c.roots, Intermediates: intermediates}); err != nil {
		c.mu.Lock()
		delete(c.certs, raw)
		c.mu.Unlock()
		return e, bad
	}
	key, ok := certs[0].PublicKey.(*rsa.PublicKey)
	if !ok {
		return e, bad
	}
	digest := sha256.Sum256(body)
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return e, bad
	}
	c.mu.Lock()
	if len(c.certs) >= 16 {
		c.certs = nil
	}
	if c.certs == nil {
		c.certs = map[string][]*x509.Certificate{}
	}
	c.certs[raw] = certs
	c.mu.Unlock()
	return e, nil
}
