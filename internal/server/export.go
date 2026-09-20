package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// The backup is the user's whole workspace as the files a development
// kakeratta reads — the same tree as KAKERATTA_HOME — so extracting it and
// pointing a server at it reproduces what they see here: posts, personas and
// their notes, who is in the room, and captures.
//
// What it leaves behind is everything that says how this person reached a
// service from this machine: the API keys and the rest of the settings, the
// Bluesky tokens, the key an external agent connects with. None of that is
// what a workspace is made of, and leaving it out is what makes the file safe
// to keep anywhere — there is no credential in it to lose. A restored
// workspace asks for a key and connects for itself.
//
// It is written as one stream and never assembled anywhere: this runs in a
// container with a gigabyte of memory and /tmp is part of that memory, so a
// workspace of captures would take the whole deployment down with it.
//
// Where the stream goes depends on the deployment. With object storage
// configured the export is written there and the user is handed a link that
// works for an hour, which keeps a long download off this process and out of
// the proxy's timeout; the page is told the link over the event stream. Alone
// on a laptop there is nowhere to put it, so it goes straight down the
// response.

// exportTimeout bounds one export. It is generous: a large workspace is read
// one capture at a time out of object storage.
const exportTimeout = 30 * time.Minute

// POST /api/export starts one export and says where it will come from.
func (s *Server) startExport(w http.ResponseWriter, r *http.Request) {
	name := exportName()
	if s.exports == nil {
		writeJSON(w, http.StatusOK, map[string]any{"state": "ready", "url": "/api/export", "name": name})
		return
	}
	if !s.exporting.CompareAndSwap(false, true) {
		fail(w, http.StatusConflict, "書き出しの途中です。終わるまでお待ちください")
		return
	}
	go s.runExport(name)
	writeJSON(w, http.StatusAccepted, map[string]any{"state": "running", "name": name})
}

// runExport writes the export into storage and tells the page about it. It
// outlives the request that asked for it, so it carries its own deadline.
func (s *Server) runExport(name string) {
	defer s.exporting.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	url, err := s.exports.Save(ctx, name, func(out io.Writer) error {
		return s.writeExport(ctx, out)
	})
	if err != nil {
		log.Printf("export %s: %v", s.store.Root(), err)
		s.publishEvent("export", map[string]any{"state": "failed", "error": "書き出しできませんでした"})
		return
	}
	s.publishEvent("export", map[string]any{"state": "ready", "url": url, "name": name})
}

// GET /api/export streams the export down the response. It is how a local
// workspace is taken, and the way back when there is nowhere to put the file.
func (s *Server) exportWorkspace(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportName()+`"`)
	w.Header().Set("Cache-Control", "no-store")
	// The status is sent with the first file, so a failure part way through can
	// only be a short download; the log says which workspace it was.
	if err := s.writeExport(r.Context(), w); err != nil {
		log.Printf("export %s: %v", s.store.Root(), err)
	}
}

func exportName() string {
	return "kakeratta-" + time.Now().Format("20060102-150405") + ".zip"
}

// writeExport turns the workspace into one zip as it is read. Nothing is held
// but the file being written.
func (s *Server) writeExport(ctx context.Context, out io.Writer) error {
	archive := zip.NewWriter(out)
	sink := zipSink{archive: archive}
	if err := s.store.Export(ctx, sink); err != nil {
		_ = archive.Close()
		return err
	}
	if err := s.markExport(sink); err != nil {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}

// zipSink writes each file of the workspace as it arrives.
type zipSink struct{ archive *zip.Writer }

func (z zipSink) Add(name string, modified time.Time, write func(io.Writer) error) error {
	// A file with no time of its own is dated by the export. Left unset it
	// unzips as 1980, which reads as a date rather than as nothing.
	if modified.IsZero() {
		modified = time.Now()
	}
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified}
	// A capture is a PNG or a JPEG. Deflating it again spends the one CPU this
	// runs on and saves nothing.
	if strings.HasPrefix(name, "blobs/") {
		header.Method = zip.Store
	}
	entry, err := z.archive.CreateHeader(header)
	if err != nil {
		return err
	}
	return write(entry)
}

// markExport adds what the file needs to be read on its own: the workspace key
// a multi-user restore places the directory under, and how to put the files
// where a development kakeratta serves them.
func (s *Server) markExport(sink store.ExportSink) error {
	manifest := struct {
		App          string    `json:"app"`
		Version      string    `json:"version"`
		ExportedAt   time.Time `json:"exportedAt"`
		WorkspaceKey string    `json:"workspaceKey,omitempty"`
	}{App: "kakeratta", Version: s.version, ExportedAt: time.Now(), WorkspaceKey: s.workspaceKey}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := addFile(sink, "backup.json", append(raw, '\n')); err != nil {
		return err
	}
	return addFile(sink, "README.txt", []byte(exportReadme))
}

func addFile(sink store.ExportSink, name string, data []byte) error {
	return sink.Add(name, time.Now(), func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

const exportReadme = `kakeratta backup / バックアップ

This zip is one user's workspace as the plain files kakeratta reads. The
layout is the same as KAKERATTA_HOME, the directory a development kakeratta
serves; the app itself is not included.

Restore / 戻し方

- Single user (task dev): extract into KAKERATTA_HOME — Settings shows the
  path as 保存先 — or set KAKERATTA_HOME to the extracted directory, then run
  ` + "`task setup` and `task dev`" + `.
- Firebase multi-user: extract under KAKERATTA_HOME/users/<workspaceKey>/;
  the key is in backup.json. Log in with the same account.

Notes / 注意

- No credentials travel with this. settings.json carries only who is in the
  room and when each one wakes, and the Bluesky and MCP connections are left
  behind, so a restored workspace asks for a key and connects for itself. /
  鍵のたぐいは入っていません。settings.json にあるのは、いる担当と起きる時刻
  だけで、BlueskyとMCPの接続も持ち出しません。復元先では自分のキーを入れて、
  つなぎ直してください。
- Semantic search rebuilds its index on the first search when the zip carries
  no cached vectors. / 意味検索のキャッシュが無いときは、最初の検索でインデックスを作り直します。
`
