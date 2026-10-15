package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/takeshy/kakeratta/internal/store"
)

const (
	judgeWorkspaceMarker = "judge-shared-mcp"
	judgeConnectionID    = "judge-shared-hermes"
	judgeConnectionName  = "Hermes"
)

// judgeSharedMCP reports whether judges get the operator's shared Hermes
// connection: only while the judge period is open and the key is usable as an
// MCP bearer.
func (g *Gateway) judgeSharedMCP() bool {
	return g.judgeActive() && len(g.options.JudgeMCPKey) == 64
}

// setUpJudgeWorkspace installs the shared connection in one judge's
// workspace and records it for discovery.
func (g *Gateway) setUpJudgeWorkspace(ctx context.Context, uid string) error {
	tenant, err := g.tenant(uid)
	if err != nil {
		return err
	}
	if err := tenant.configureJudge(ctx, g.options.JudgeMCPKey); err != nil {
		return err
	}
	if g.options.RecordJudgeWorkspace != nil {
		return g.options.RecordJudgeWorkspace(ctx, workspaceKey(uid))
	}
	return nil
}

// configureJudge is deliberately idempotent. The marker is written last, so
// discovery cannot expose a partly initialized workspace after a failed run.
// A connection the judge made themselves is kept as it is: the shared one is
// put in use only when no other is, and a repeat registration does not
// change which connection the judge has chosen.
func (s *Server) configureJudge(ctx context.Context, key string) error {
	digest := sha256.Sum256([]byte(key))
	for attempts := 0; ; attempts++ {
		v, err := s.store.SocialValue(ctx, mcpCredential)
		if err != nil {
			return err
		}
		rows, err := decodeMCPConnections(v.Data)
		if err != nil {
			return err
		}
		found, active := false, false
		for i := range rows {
			active = active || rows[i].Active
			if rows[i].ID == judgeConnectionID {
				rows[i].Name, rows[i].Digest, found = judgeConnectionName, digest[:], true
			}
		}
		if !found {
			rows = append(rows, mcpConnectionRecord{ID: judgeConnectionID, Name: judgeConnectionName, Digest: digest[:], Active: !active})
		}
		data, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		ok, err := s.store.SwapSocialValue(ctx, mcpCredential, v.Version, store.SocialValue{Data: data})
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if attempts == 4 {
			return context.DeadlineExceeded
		}
	}
	settings := s.store.ConfiguredSettings()
	if settings.Planners == nil {
		settings.Planners = make(map[string]string)
	}
	for _, id := range s.store.Settings().Active {
		settings.Planners[id] = store.PlannerExternal
	}
	settings.ExternalAgent.Paused = false
	if err := s.store.SaveSettings(settings); err != nil {
		return err
	}
	for attempts := 0; attempts < 5; attempts++ {
		v, err := s.store.SocialValue(ctx, judgeWorkspaceMarker)
		if err != nil {
			return err
		}
		ok, err := s.store.SwapSocialValue(ctx, judgeWorkspaceMarker, v.Version, store.SocialValue{Data: []byte{1}})
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return context.DeadlineExceeded
}

func (s *Server) isConfiguredJudge(ctx context.Context, digest [32]byte) bool {
	marker, err := s.store.SocialValue(ctx, judgeWorkspaceMarker)
	if err != nil || len(marker.Data) == 0 {
		return false
	}
	rows, err := s.mcpConnections(ctx)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.ID == judgeConnectionID && row.Active && same(string(row.Digest), string(digest[:])) {
			return true
		}
	}
	return false
}

// judgeWorkspaces lists the workspaces the shared judge Hermes serves. Only
// recorded judge workspaces are looked at, each opened the way a request
// opens it, so a workspace being deleted is skipped rather than touched.
func (g *Gateway) judgeWorkspaces(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key := bearer(r.Header.Get("Authorization"))
	if !g.judgeSharedMCP() || len(key) != 64 || !same(key, g.options.JudgeMCPKey) {
		fail(w, http.StatusUnauthorized, "invalid judge credential")
		return
	}
	list := g.options.JudgeWorkspaces
	if list == nil {
		list = g.listWorkspaces
	}
	keys, err := list(r.Context())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "workspace listing unavailable")
		return
	}
	// The gate joins each path to the host it called; mcpUrl is only a hint,
	// and is never built from a header the caller can set.
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("KAKERATTA_PUBLIC_URL")), "/")
	if parsed, err := url.Parse(base); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		scheme := "http"
		if requestIsHTTPS(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	digest := sha256.Sum256([]byte(key))
	workspaces := make([]map[string]string, 0)
	for _, workspace := range keys {
		if !validWorkspaceKey(workspace) {
			continue
		}
		if g.shared != nil && g.shared.Exists != nil {
			if exists, err := g.shared.Exists(r.Context(), workspace); err != nil || !exists {
				continue
			}
		}
		tenant, err := g.workspace(workspace)
		if err != nil {
			continue
		}
		if tenant.isConfiguredJudge(r.Context(), digest) {
			path := "/api/mcp/users/" + workspace
			workspaces = append(workspaces, map[string]string{"workspaceId": workspace, "mcpPath": path, "mcpUrl": base + path})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": workspaces})
}
