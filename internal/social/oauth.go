package social

import (
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
)

const MetadataPath = "/api/social/bluesky/client-metadata.json"
const CallbackPath = "/api/social/bluesky/callback"

var Scopes = []string{"atproto", "repo:app.bsky.feed.post?action=create", "blob:image/*"}

func OAuthConfig() (oauth.ClientConfig, []byte, error) {
	var config oauth.ClientConfig
	origin := strings.TrimRight(strings.TrimSpace(os.Getenv("KAKERATTA_PUBLIC_URL")), "/")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return config, nil, errors.New("Bluesky連携はまだ準備されていません")
	}
	local := u.Scheme == "http" && u.Hostname() == "127.0.0.1"
	if u.Scheme != "https" && !local {
		return config, nil, errors.New("Bluesky連携には公開HTTPS URLが必要です")
	}
	secret, err := hex.DecodeString(os.Getenv("KAKERATTA_BLUESKY_KEY"))
	if err != nil || len(secret) != 32 {
		return config, nil, errors.New("Bluesky連携のサーバー鍵が設定されていません")
	}
	private, err := atcrypto.ParsePrivateBytesP256(secret)
	if err != nil {
		return config, nil, errors.New("Bluesky連携のサーバー鍵が不正です")
	}
	if local {
		config = oauth.NewLocalhostConfig(origin+CallbackPath, Scopes)
	} else {
		config = oauth.NewPublicConfig(origin+MetadataPath, origin+CallbackPath, Scopes)
		if err := config.SetClientSecret(private, "kakeratta-1"); err != nil {
			return config, nil, err
		}
	}
	config.UserAgent = "kakeratta"
	return config, secret, nil
}
func Metadata(config oauth.ClientConfig) oauth.ClientMetadata {
	metadata := config.ClientMetadata()
	name := "kakeratta"
	metadata.ClientName = &name
	if config.IsConfidential() {
		keys := config.PublicJWKS()
		metadata.JWKS = &keys
	}
	return metadata
}
