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
	_ "embed"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/takeshy/kakeratta/internal/alarm"
	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/server"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// version is set at link time.
var version = "dev"

// The Hermes integration is served over MCP as well as from the page's host:
// a sandbox that allows only the MCP endpoint updates through list_asks and
// get_integration. The files are the ones the page publishes.
//
//go:embed frontend/public/integrations/hermes/kakeratta/SKILL.md
var hermesSkill string

//go:embed frontend/public/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py
var hermesGate string

func main() {
	server.SetIntegration(hermesSkill, hermesGate)
	root := os.Getenv("KAKERATTA_HOME")
	if root == "" {
		root = store.DefaultRoot()
	}
	var err error
	var handler http.Handler
	var closeApp func()
	// Where the tools go is the operator's: one set of map endpoints and one
	// list of MCP servers for every user of this deployment.
	answerRouter, err := persona.AnswerRouterFromEnv()
	if err != nil {
		log.Fatal(err)
	}
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
		judgeExpiresAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(os.Getenv("KAKERATTA_JUDGE_EXPIRES_AT")))
		judgeMaxUses, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("KAKERATTA_JUDGE_MAX_USES")))
		authOptions := server.AuthOptions{
			Root: root, Version: version,
			FirebaseProjectID:  firebaseProjectID,
			FirebaseAPIKey:     strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_API_KEY")),
			FirebaseAuthDomain: strings.TrimSpace(os.Getenv("KAKERATTA_FIREBASE_AUTH_DOMAIN")),
			Tools:              toolConfig,
			AnswerRouter:       answerRouter,
			JudgeInviteToken:   strings.TrimSpace(os.Getenv("KAKERATTA_JUDGE_INVITE_TOKEN")),
			JudgeAPIKey:        strings.TrimSpace(os.Getenv("KAKERATTA_JUDGE_TOKEN_FACTORY_KEY")),
			JudgeExpiresAt:     judgeExpiresAt,
			JudgeMaxUses:       judgeMaxUses,
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
			authOptions.ClaimJudgeUse = func(ctx context.Context, uid string, limit int) (int, bool, error) {
				ref := firestoreClient.Collection("judgeUsage").Doc(uid)
				count := 0
				allowed := false
				err := firestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
					count, allowed = 0, false
					snapshot, err := tx.Get(ref)
					if err == nil {
						if value, ok := snapshot.Data()["count"].(int64); ok {
							count = int(value)
						}
					} else if status.Code(err) != codes.NotFound {
						return err
					}
					if count >= limit {
						return nil
					}
					count++
					allowed = true
					return tx.Set(ref, map[string]any{"count": count, "updatedAt": firestore.ServerTimestamp}, firestore.MergeAll)
				})
				return count, allowed, err
			}
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
			// Instances share the users through Firestore: any of them
			// answers any request, and each workspace's persona work is
			// taken up by whichever instance claims it.
			authOptions.Shared = &server.Shared{
				Watch: func(ctx context.Context, queued func(string)) {
					store.WatchReactions(ctx, firestoreClient, queued)
				},
				Pending: func(ctx context.Context) (map[string]bool, error) {
					return store.ClaimableWorkspaces(ctx, firestoreClient)
				},
				Claimable: func(ctx context.Context, key string) (bool, error) {
					return store.WorkspaceClaimable(ctx, firestoreClient, key)
				},
				Exists: func(ctx context.Context, key string) (bool, error) {
					return store.WorkspaceExists(ctx, firestoreClient, key)
				},
				Closing: func(ctx context.Context, key string) (bool, error) {
					return store.WorkspaceClosing(ctx, firestoreClient, key)
				},
			}
			if cloudBlobs == nil {
				log.Print("KAKERATTA_GCS_BUCKET が未設定です。画像はインスタンスごとの一時ディレクトリに置かれ、複数インスタンスでは共有されません")
			}
			// A workspace is woken by a Cloud Tasks task at the next time
			// anything of its comes due, and the task comes back to
			// /internal/due. Without a queue, timers in each process do it.
			queue, dueURL, account := os.Getenv("KAKERATTA_TASKS_QUEUE"), os.Getenv("KAKERATTA_TASKS_URL"), os.Getenv("KAKERATTA_TASKS_ACCOUNT")
			if queue != "" {
				tasks, err := alarm.NewTasks(context.Background(), queue, dueURL, account)
				if err != nil {
					log.Fatalf("Cloud Tasksを初期化できません: %v", err)
				}
				authOptions.Shared.Alarm = tasks.Set
				authOptions.Shared.Verify = func(r *http.Request) (string, error) {
					return alarm.Workspace(r, alarm.GoogleValidator, dueURL, account)
				}
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
			Store: workspace, Catalogue: persona.NewCatalogue(workspace),
			Version:      version,
			Tools:        toolConfig,
			AnswerRouter: answerRouter,
		}
		if cloudBlobs != nil {
			options.Exports = cloudBlobs.Exports("exports/single")
		}
		app := server.New(options)
		handler, closeApp = app, app.Halt
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
	// Stop taking posts first, then finish the reaction in hand. The rest of
	// the queue is kept: the next start, or another instance, takes it up.
	closeApp()
}
