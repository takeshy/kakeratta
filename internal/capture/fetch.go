package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Fetched is a public page, reduced to something a persona can read.
type Fetched struct {
	Title string
	Text  string
	URL   string
}

// Fetch pulls a public page.
//
// This is the secondary way in, and it has a hard limit: fetching from the
// server means there is no session, so anything behind a login comes back as
// the login page. That is why the capture is the main route - a screenshot does
// not care whether the user was signed in.
func Fetch(ctx context.Context, target string) (Fetched, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return Fetched{}, errors.New("http/https のURLを渡してください")
	}
	if private(parsed.Hostname()) {
		// The relay sits on a server with its own network around it. A URL is
		// user input, and user input must not be a way to reach that network.
		return Fetched{}, errors.New("そのURLは取得できません")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Fetched{}, err
	}
	request.Header.Set("User-Agent", "kakeratta/capture-timeline")
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("リダイレクトが多すぎます")
			}
			if private(r.URL.Hostname()) {
				return errors.New("そのURLは取得できません")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return Fetched{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return Fetched{}, fmt.Errorf("取得できませんでした (%d)", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return Fetched{}, err
	}
	html := string(raw)
	return Fetched{Title: title(html), Text: text(html), URL: parsed.String()}, nil
}

func private(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// A name that does not resolve is not worth trying, and refusing is the
		// safe half of the guess.
		return true
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func title(html string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, "<title")
	if start < 0 {
		return ""
	}
	open := strings.IndexByte(html[start:], '>')
	if open < 0 {
		return ""
	}
	start += open + 1
	end := strings.Index(lower[start:], "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(unescape(html[start : start+end]))
}

// text strips a page down to its words.
//
// This is deliberately crude. A persona reads the result the way it reads a
// capture's OCR, and neither is expected to be the page's structure - just what
// it said.
func text(html string) string {
	var out strings.Builder
	skip := ""
	for i := 0; i < len(html); {
		if html[i] != '<' {
			out.WriteByte(html[i])
			i++
			continue
		}
		end := strings.IndexByte(html[i:], '>')
		if end < 0 {
			break
		}
		tag := strings.ToLower(strings.TrimSpace(html[i+1 : i+end]))
		name := tag
		if space := strings.IndexAny(name, " \t\n/"); space > 0 {
			name = name[:space]
		}
		switch {
		case skip != "":
			if name == "/"+skip {
				skip = ""
			}
		case name == "script", name == "style", name == "noscript", name == "svg":
			skip = name
		case name == "br", name == "/p", name == "/div", name == "/li", name == "/h1",
			name == "/h2", name == "/h3", name == "/tr":
			out.WriteByte('\n')
		}
		i += end + 1
	}
	return tidy(unescape(out.String()))
}

func unescape(text string) string {
	return strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'",
		"&nbsp;", " ", "&#x27;", "'", "&apos;", "'",
	).Replace(text)
}

// tidy collapses the whitespace a stripped page is mostly made of.
func tidy(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '\t' || r == '\r' || (unicode.IsSpace(r) && r != ' ') {
				return ' '
			}
			return r
		}, line))
		for strings.Contains(line, "  ") {
			line = strings.ReplaceAll(line, "  ", " ")
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	joined := strings.Join(lines, "\n")
	if runes := []rune(joined); len(runes) > 20000 {
		joined = string(runes[:20000]) + "\n（以下省略）"
	}
	return joined
}
