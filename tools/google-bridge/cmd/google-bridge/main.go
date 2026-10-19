// google-bridge is a separate personal Web application and MCP server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	googletools "github.com/takeshy/kakeratta/tools/google-bridge/internal/google"
	"github.com/takeshy/kakeratta/tools/google-bridge/internal/persistence"
	"github.com/takeshy/kakeratta/tools/google-bridge/internal/server"
)

type configuration struct {
	Listen     string            `json:"listen"`
	PublicURL  string            `json:"publicUrl"`
	DataDir    string            `json:"dataDir"`
	AdminToken string            `json:"adminToken"`
	KeyVersion string            `json:"keyVersion"`
	Keys       map[string]string `json:"keys"`
}

func randomHex() (string, error) {
	v := make([]byte, 32)
	if _, err := rand.Read(v); err != nil {
		return "", err
	}
	return hex.EncodeToString(v), nil
}
func initialize(path string) error {
	base, err := os.UserConfigDir()
	if err != nil {
		return errors.New("Could not locate user configuration directory")
	}
	admin, err := randomHex()
	if err != nil {
		return err
	}
	key, err := randomHex()
	if err != nil {
		return err
	}
	cfg := configuration{Listen: "127.0.0.1:8094", PublicURL: "http://127.0.0.1:8094", DataDir: filepath.Join(base, "google-bridge-data"), AdminToken: admin, KeyVersion: "v1", Keys: map[string]string{"v1": key}}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("Could not create configuration directory")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("Configuration already exists or cannot be created")
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(cfg); err != nil {
		return errors.New("Could not write configuration")
	}
	return f.Sync()
}
func load(path string) (configuration, error) {
	var cfg configuration
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return cfg, errors.New("Configuration must exist as an owner-only file (chmod 600); first run with -init")
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("Could not read configuration")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil {
		return cfg, errors.New("Invalid configuration JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return cfg, errors.New("Invalid configuration JSON")
	}
	if !filepath.IsAbs(cfg.DataDir) {
		return cfg, errors.New("dataDir must be absolute")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return cfg, errors.New("Invalid configuration path")
	}
	relative, err := filepath.Rel(cfg.DataDir, abs)
	if err != nil {
		return cfg, errors.New("Invalid data directory")
	}
	if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return cfg, errors.New("Store the encryption configuration outside dataDir")
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return cfg, errors.New("Invalid listen address")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) && !strings.HasPrefix(cfg.PublicURL, "https://") {
		return cfg, errors.New("Non-loopback listeners require a public HTTPS origin and TLS reverse proxy")
	}
	return cfg, nil
}
func run() error {
	base, err := os.UserConfigDir()
	if err != nil {
		return errors.New("Could not locate configuration directory")
	}
	path := flag.String("config", filepath.Join(base, "google-bridge", "config.json"), "Owner-only configuration file, outside the data directory")
	init := flag.Bool("init", false, "Create a configuration with random management and encryption keys")
	flag.Parse()
	if *init {
		if err := initialize(*path); err != nil {
			return err
		}
		fmt.Printf("Created %s (mode 0600). Use its adminToken to sign in; never paste it into chat.\nRun this command again without -init.\n", *path)
		return nil
	}
	cfg, err := load(*path)
	if err != nil {
		return err
	}
	keys := map[string][]byte{}
	for name, value := range cfg.Keys {
		key, err := hex.DecodeString(value)
		if err != nil {
			return errors.New("Invalid encryption key")
		}
		keys[name] = key
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return errors.New("Could not open data directory")
	}
	lock, err := lockDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	defer lock()
	w, err := store.Open(cfg.DataDir)
	if err != nil {
		return errors.New("Could not open credential workspace")
	}
	app, err := googlebridge.New(persistence.New(w), googlebridge.Config{AdminToken: cfg.AdminToken, Google: googletools.Config{Origin: cfg.PublicURL, CurrentKey: cfg.KeyVersion, Keys: keys}})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("Personal Google Bridge: %s\n", cfg.PublicURL)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("Could not start HTTP server")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
