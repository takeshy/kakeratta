// Command dokking is the relay.
//
// The client is a page in a browser, and a browser cannot hold the keys: the
// inference credentials, the search credentials and the reaction loop all live
// here. It also has to be a server for a second reason - a share sheet posts
// multipart to a URL, and static hosting cannot take that.
//
// It is one binary with the page embedded, so running it locally and deploying
// it are the same thing.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/takeshy/dokking/internal/persona"
	"github.com/takeshy/dokking/internal/server"
	"github.com/takeshy/dokking/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set at link time.
var version = "dev"

func main() {
	root := os.Getenv("DOKKING_HOME")
	if root == "" {
		root = store.DefaultRoot()
	}
	workspace, err := store.Open(root)
	if err != nil {
		log.Fatalf("ワークスペースを開けません: %v", err)
	}

	pages, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("画面を読み込めません: %v", err)
	}

	catalogue := persona.NewCatalogue(workspace.SkillDir())
	app := server.New(server.Options{
		Store:     workspace,
		Catalogue: catalogue,
		Assets:    pages,
		Version:   version,
		// A token is how the iOS shortcut and anything else outside the browser
		// identifies itself. Left empty, the app is open - which is right on a
		// laptop and wrong the moment it is deployed, so the log says so.
		Token: strings.TrimSpace(os.Getenv("DOKKING_TOKEN")),
	})

	address := os.Getenv("DOKKING_ADDR")
	if address == "" {
		address = ":8787"
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           app,
		ReadHeaderTimeout: 15 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		log.Printf("dokking %s — %s  （保存先 %s）", version, address, root)
		if os.Getenv("DOKKING_TOKEN") == "" {
			log.Print("DOKKING_TOKEN が未設定です。手元だけで動かしてください。")
		}
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}
