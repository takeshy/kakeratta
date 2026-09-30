package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
)

// Credentials are stored as hashes only. The legacy single credential becomes
// the default connection without requiring users to replace their existing key.
type mcpConnectionRecord struct {
	Active bool   `json:"active"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Digest []byte `json:"digest"`
	// How long an ask waits for this agent to claim it, then to answer. Zero
	// is the app-wide setting, which is where the waits used to live.
	ClaimMinutes int `json:"claimMinutes,omitempty"`
	PlanMinutes  int `json:"planMinutes,omitempty"`
}

func decodeMCPConnections(data []byte) ([]mcpConnectionRecord, error) {
	if len(data) == 0 {
		return []mcpConnectionRecord{}, nil
	}
	if len(data) == 32 {
		return []mcpConnectionRecord{{ID: "default", Name: "Hermes", Digest: data, Active: true}}, nil
	}
	var rows []mcpConnectionRecord
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Server) mcpConnections(ctx context.Context) ([]mcpConnectionRecord, error) {
	v, err := s.store.SocialValue(ctx, mcpCredential)
	if err != nil {
		return nil, err
	}
	return decodeMCPConnections(v.Data)
}

func (s *Server) changeMCPConnection(ctx context.Context, id, name string, digest []byte, create bool) error {
	v, err := s.store.SocialValue(ctx, mcpCredential)
	if err != nil {
		return err
	}
	rows, err := decodeMCPConnections(v.Data)
	if err != nil {
		return err
	}
	found := -1
	for i := range rows {
		if rows[i].ID == id {
			found = i
			break
		}
	}
	if found < 0 && !create {
		return fmt.Errorf("接続が見つかりません")
	}
	if len(digest) == 0 {
		if found >= 0 {
			wasActive := rows[found].Active
			rows = append(rows[:found], rows[found+1:]...)
			// There is no choosing none: the connection in use gone, the
			// first one left takes its place.
			if wasActive && len(rows) > 0 {
				rows[0].Active = true
			}
		}
	} else {
		if strings.TrimSpace(name) == "" && found >= 0 {
			name = rows[found].Name
		}
		if strings.TrimSpace(name) == "" {
			name = "Hermes"
		}
		if len([]rune(name)) > 80 {
			return fmt.Errorf("接続名は80文字以内です")
		}
		row := mcpConnectionRecord{ID: id, Name: strings.TrimSpace(name), Digest: digest, Active: len(rows) == 0}
		if found >= 0 {
			row.Active = rows[found].Active
			row.ClaimMinutes, row.PlanMinutes = rows[found].ClaimMinutes, rows[found].PlanMinutes
		}
		if found >= 0 {
			rows[found] = row
		} else {
			for i := range rows {
				rows[i].Active = false
			}
			row.Active = true
			rows = append(rows, row)
		}
	}
	var data []byte
	if len(rows) > 0 {
		data, err = json.Marshal(rows)
		if err != nil {
			return err
		}
	}
	ok, err := s.store.SwapSocialValue(ctx, mcpCredential, v.Version, store.SocialValue{Data: data})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("接続設定が更新されています。再試行してください")
	}
	return nil
}

func (s *Server) publicMCPConnections(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.mcpConnections(ctx)
	if err != nil {
		return nil, err
	}
	agent := s.store.Settings().ExternalAgent
	out := []map[string]any{}
	for _, row := range rows {
		out = append(out, map[string]any{"id": row.ID, "name": row.Name, "active": row.Active, "configured": true, "path": "/api/mcp/users/" + s.mcpWorkspace(),
			"claimMinutes": store.WaitMinutesOr(row.ClaimMinutes, store.WaitMinutesOr(agent.ClaimMinutes, store.DefaultClaimMinutes)),
			"planMinutes":  store.WaitMinutesOr(row.PlanMinutes, store.WaitMinutesOr(agent.PlanMinutes, store.DefaultPlanMinutes))})
	}
	return out, nil
}

func (s *Server) selectMCPConnection(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "invalid origin")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, "設定を読めませんでした")
		return
	}
	v, err := s.store.SocialValue(r.Context(), mcpCredential)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	rows, err := decodeMCPConnections(v.Data)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	found := body.ID == ""
	changed := false
	for i := range rows {
		active := rows[i].ID == body.ID
		if active {
			found = true
		}
		if rows[i].Active != active {
			changed = true
		}
		rows[i].Active = active
	}
	if !found {
		fail(w, 404, "接続が見つかりません")
		return
	}
	data, _ := json.Marshal(rows)
	ok, err := s.store.SwapSocialValue(r.Context(), mcpCredential, v.Version, store.SocialValue{Data: data})
	if err != nil || !ok {
		fail(w, 409, "接続設定が更新されています。再試行してください")
		return
	}
	if changed && s.runner != nil {
		s.runner.ResetExternalAgent()
	}
	s.mcpConnection(w, r)
}

// activeMCPWaits are the waits of the connection in use, zero where it has
// none of its own.
func (s *Server) activeMCPWaits() (time.Duration, time.Duration) {
	rows, err := s.mcpConnections(context.Background())
	if err != nil {
		return 0, 0
	}
	for _, row := range rows {
		if row.Active {
			return time.Duration(row.ClaimMinutes) * time.Minute, time.Duration(row.PlanMinutes) * time.Minute
		}
	}
	return 0, 0
}

// setMCPConnectionWaits changes how long asks wait for one connection's agent.
func (s *Server) setMCPConnectionWaits(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "invalid origin")
		return
	}
	var body struct {
		ID           string `json:"id"`
		ClaimMinutes int    `json:"claimMinutes"`
		PlanMinutes  int    `json:"planMinutes"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, "設定を読めませんでした")
		return
	}
	v, err := s.store.SocialValue(r.Context(), mcpCredential)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	rows, err := decodeMCPConnections(v.Data)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	found := false
	for i := range rows {
		if rows[i].ID != body.ID {
			continue
		}
		found = true
		if body.ClaimMinutes > 0 {
			rows[i].ClaimMinutes = store.WaitMinutesOr(body.ClaimMinutes, store.DefaultClaimMinutes)
		}
		if body.PlanMinutes > 0 {
			rows[i].PlanMinutes = store.WaitMinutesOr(body.PlanMinutes, store.DefaultPlanMinutes)
		}
	}
	if !found {
		fail(w, 404, "接続が見つかりません")
		return
	}
	data, _ := json.Marshal(rows)
	ok, err := s.store.SwapSocialValue(r.Context(), mcpCredential, v.Version, store.SocialValue{Data: data})
	if err != nil || !ok {
		fail(w, 409, "接続設定が更新されています。再試行してください")
		return
	}
	s.mcpConnection(w, r)
}
