package main

import (
	"embed"
	"encoding/json"
	"log"
	"os"

	"github.com/takeshy/dokking/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed wails.json
var wailsJSON []byte

//go:embed build/appicon.png
var appIcon []byte

// version can be set at link time; otherwise the embedded manifest is the
// single source of truth, so a version bump is one commit.
var version = ""

func appVersion() string {
	if version != "" {
		return version
	}
	var manifest struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(wailsJSON, &manifest); err == nil && manifest.Info.Version != "" {
		return manifest.Info.Version
	}
	return "dev"
}

func main() {
	root := os.Getenv("DOKKING_HOME")
	if root == "" {
		root = store.DefaultRoot()
	}
	workspace, err := store.Open(root)
	if err != nil {
		log.Fatalf("ワークスペースを開けません: %v", err)
	}

	wailsApp := application.New(application.Options{
		Name:        "dokking",
		Description: "Reading app whose AI has read exactly as far as you have",
		Icon:        appIcon,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})
	wailsApp.RegisterService(application.NewService(NewApp(wailsApp, workspace)))

	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "dokking",
		Width:  1100,
		Height: 780,
		URL:    "/",
		// Memos can be dictated while listening, so the microphone is granted
		// here rather than through a WebView prompt mid-sentence.
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone: application.PermissionAllow,
		},
		Linux: application.LinuxWindow{Icon: appIcon},
	})

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
}
