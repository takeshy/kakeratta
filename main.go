// Command kakeratta is the relay.
//
// The client is a page in a browser, and a browser cannot hold the keys: the
// inference credentials, the search credentials and the reaction loop all live
// here. It also has to be a server for a second reason - a share sheet posts
// multipart to a URL, and static hosting cannot take that.
//
// The page is not in here. It is a static site of its own - Vite serves it in
// development, Firebase Hosting in production - and this binary is only what
// /api/** reaches. Either can be replaced without touching the other.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/server"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
	"google.golang.org/api/iterator"
)

// version is set at link time.
var version = "dev"

func main() {
	root := os.Getenv("KAKERATTA_HOME")
	if root == "" {
		root = store.DefaultRoot()
	}
	var err error
	var handler http.Handler
	var closeApp func()
	// Where the tools go is the operator's: one set of map endpoints and one
	// list of MCP servers for every user of this deployment.
	toolConfig, err := tools.ConfigFromEnv(version)
	if err != nil {
		log.Fatal(err)
	}
	if servers := toolConfig.MCP.Servers(); len(servers) > 0 {
		log.Printf("MCPサーバー: %s", strings.Join(servers, ", "))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := toolConfig.MCP.Refresh(ctx); err != nil {
				log.Printf("MCPの道具一覧を取得できませんでした（あとで再試行します）: %v", err)
			}
		}()
	}
	firebaseProjectID := strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_PROJECT_ID"))
	var cloudBlobs *store.GCSBlobs
	if bucket := strings.TrimSpace(os.Getenv("KAKERATTA_GCS_BUCKET")); bucket != "" {
		cloudBlobs, err = store.NewGCSBlobs(context.Background(), bucket)
		if err != nil {
			log.Fatalf("Cloud Storageを初期化できません: %v", err)
		}
		defer cloudBlobs.Close()
	}
	var firestoreClient *firestore.Client
	if strings.EqualFold(strings.TrimSpace(os.Getenv("KAKERATTA_STORE")), "firestore") {
		projectID := strings.TrimSpace(os.Getenv("KAKERATTA_FIRESTORE_PROJECT_ID"))
		if projectID == "" {
			projectID = firebaseProjectID
		}
		if projectID == "" {
			log.Fatal("Firestoreにはproject IDが必要です")
		}
		firestoreClient, err = firestore.NewClient(context.Background(), projectID)
		if err != nil {
			log.Fatalf("Firestoreを初期化できません: %v", err)
		}
		defer firestoreClient.Close()
	}
	if firebaseProjectID != "" {
		authOptions := server.AuthOptions{
			Root: root, Version: version,
			FirebaseProjectID:  firebaseProjectID,
			FirebaseAPIKey:     strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_API_KEY")),
			FirebaseAuthDomain: strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_AUTH_DOMAIN")),
			Tools:              toolConfig,
			BlobStore: func(userKey string) store.BlobStore {
				if cloudBlobs == nil {
					return nil
				}
				return cloudBlobs.Scope("users/" + userKey + "/blobs")
			},
			// Backups sit under their own prefix, which the bucket sweeps on a
			// lifecycle rule: a download link is worth an hour, not a copy of
			// everyone's workspace kept for good.
			ExportTarget: func(userKey string) store.ExportTarget {
				if cloudBlobs == nil {
					return nil
				}
				return cloudBlobs.Exports("exports/" + userKey)
			},
		}
		if firestoreClient != nil {
			authOptions.ListWorkspaces = func(ctx context.Context) ([]string, error) {
				iter := firestoreClient.Collection("users").Documents(ctx)
				defer iter.Stop()
				var keys []string
				for {
					doc, err := iter.Next()
					if errors.Is(err, iterator.Done) {
						return keys, nil
					}
					if err != nil {
						return nil, err
					}
					keys = append(keys, doc.Ref.ID)
				}
			}
			authOptions.OpenWorkspace = func(userKey string, blobs store.BlobStore) (store.Workspace, error) {
				return store.NewFirestoreWorkspace(context.Background(), firestoreClient, userKey, blobs)
			}
		}
		app, err := server.NewGateway(context.Background(), authOptions)
		if err != nil {
			log.Fatalf("Firebase認証を初期化できません: %v", err)
		}
		handler, closeApp = app, app.Close
	} else {
		workspace, err := store.Open(root)
		if err != nil {
			log.Fatalf("ワークスペースを開けません: %v", err)
		}
		if cloudBlobs != nil {
			workspace.SetBlobStore(cloudBlobs.Scope("single/blobs"))
		}
		options := server.Options{
			Store: workspace, Catalogue: persona.NewCatalogue(workspace.SkillDir()),
			Version: version,
			Tools:   toolConfig,
		}
		if cloudBlobs != nil {
			options.Exports = cloudBlobs.Exports("exports/single")
		}
		app := server.New(options)
		handler, closeApp = app, app.Close
	}

	address := os.Getenv("KAKERATTA_ADDR")
	if address == "" {
		address = "127.0.0.1:8787"
		if firebaseProjectID != "" {
			address = ":8787"
		}
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		log.Printf("kakeratta %s — %s", version, address)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	// Stop taking posts first, then let the reactions already in hand finish.
	closeApp()
}
