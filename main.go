// Command kakeratta is the relay.
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

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/server"
	"github.com/takeshy/kakeratta/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set at link time.
var version = "dev"

func main() {
	root := os.Getenv("KAKERATTA_HOME")
	if root == "" {
		root = store.DefaultRoot()
	}
	pages, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("画面を読み込めません: %v", err)
	}

	var handler http.Handler
	var closeApp func()
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
			Root: root, Assets: pages, Version: version,
			FirebaseProjectID:  firebaseProjectID,
			FirebaseAPIKey:     strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_API_KEY")),
			FirebaseAuthDomain: strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_AUTH_DOMAIN")),
			BlobStore: func(userKey string) store.BlobStore {
				if cloudBlobs == nil {
					return nil
				}
				return cloudBlobs.Scope("users/" + userKey + "/blobs")
			},
		}
		if firestoreClient != nil {
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
		app := server.New(server.Options{
			Store: workspace, Catalogue: persona.NewCatalogue(workspace.SkillDir()),
			Assets: pages, Version: version,
			Token: strings.TrimSpace(os.Getenv("KAKERATTA_TOKEN")),
		})
		handler, closeApp = app, app.Close
	}

	address := os.Getenv("KAKERATTA_ADDR")
	if address == "" {
		address = ":8787"
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		log.Printf("kakeratta %s — %s  （保存先 %s）", version, address, root)
		if os.Getenv("KAKERATTA_TOKEN") == "" {
			log.Print("KAKERATTA_TOKEN が未設定です。手元だけで動かしてください。")
		}
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
