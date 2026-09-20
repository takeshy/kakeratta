package server

import (
	"context"
	"net/http"
)

// Closing an account is the one request here that removes rather than appends.
// It is the gateway's to answer: a hosted workspace is one tenant among many,
// and closing it means shutting that tenant down, taking its data wherever the
// deployment put it, and forgetting the login. A server alone on a laptop has
// no account to close and cannot stop itself; the directory it serves is the
// user's to remove.

// destroy removes everything this workspace holds: the store, with captures
// wherever the blob store keeps them, and the backups written for download.
// The server is closed first, so nothing is reacting while it runs.
func (s *Server) destroy(ctx context.Context) error {
	if err := s.store.Destroy(ctx); err != nil {
		return err
	}
	if s.exports != nil {
		return s.exports.Purge(ctx)
	}
	return nil
}

// DELETE /api/workspace reaches a Server only without a gateway in front: the
// single-user deployment, where the workspace is a directory on this machine.
func (s *Server) destroyWorkspace(w http.ResponseWriter, _ *http.Request) {
	fail(w, http.StatusConflict, "手元のワークスペースは、アプリを止めてから保存先のディレクトリを削除してください")
}
