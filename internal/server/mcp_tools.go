package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/timeline"
)

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"inputSchema"`
	Annotations map[string]bool `json:"annotations"`
}

// Preserve state/claim for existing clients and stored tasks while exposing
// explicit recovery metadata in both list_tasks and claim_task responses.
type mcpAgentTask struct {
	store.AgentTask
	Status  string `json:"status"`
	ClaimID string `json:"claimId,omitempty"`
}

func agentTaskResponse(task store.AgentTask) mcpAgentTask {
	return mcpAgentTask{AgentTask: task, Status: task.State, ClaimID: task.Claim}
}

func kakerattaMCPTools() []mcpTool {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	tool := func(name, description string, readOnly bool, properties map[string]any, required ...string) mcpTool {
		if required == nil {
			required = []string{}
		}
		return mcpTool{Name: name, Description: description, InputSchema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}, Annotations: map[string]bool{"readOnlyHint": readOnly, "destructiveHint": name == "write_note", "idempotentHint": true, "openWorldHint": false}}
	}
	return []mcpTool{
		tool("list_personas", "List this user's personas and their domains.", true, map[string]any{}),
		tool("get_timeline", "Read a page of this user's timeline. Use next as before to continue.", true, map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}, "before": str("Pagination cursor")}),
		tool("get_thread", "Read the conversation containing an entry in this user's timeline.", true, map[string]any{"entryId": str("Entry ID")}, "entryId"),
		tool("list_notes", "List note paths for one of this user's personas.", true, map[string]any{"persona": str("Persona ID")}, "persona"),
		tool("read_note", "Read a persona's Markdown note. Links starting / are relative to that persona's note bundle.", true, map[string]any{"persona": str("Persona ID"), "path": str("Bundle-relative note path")}, "persona", "path"),
		tool("write_note", "Save a complete Markdown note in the Hermes persona's own bundle. Other personas' notes are read-only. Read an existing note before replacing it.", false, map[string]any{"path": str("Note path, e.g. index.md or tasks/readme.md"), "text": str("Complete Markdown"), "sourceEntryId": str("Optional source entry ID")}, "path", "text"),
		tool("list_tasks", "Read pending and claimed requests with status, claimId and claimedAt (null for legacy claims with unknown acquisition time). Completed results awaiting delivery are recovered. This tool does not run your agent. Claimed tasks must not be executed again just because you reconnect.", false, map[string]any{}),
		tool("claim_task", "Claim a pending request before executing it. Choose a unique claimId per execution and reuse it when retrying this call. Another claimId cannot take the same task. Claims do not expire. A claimed task cannot be taken with another claimId. If execution outcome is unknown after a crash, do not repeat the work; ask the user to verify external effects and cancel before submitting a new request.", false, map[string]any{"taskId": str("Task ID"), "claimId": str("Stable client-generated execution ID, max 200 characters")}, "taskId", "claimId"),
		tool("cancel_task", "Cancel a pending or claimed request only at the user's direction. To retry uncertain work, the user must verify external effects and submit a new request after cancellation. This cannot stop external work already running.", false, map[string]any{"taskId": str("Task ID")}, "taskId"),
		tool("complete_task", "Finish a claimed request and post the result to its original thread. An empty text completes quietly. Retries reuse the first accepted result; never repeat external work because delivery failed.", false, map[string]any{"taskId": str("Task ID"), "claimId": str("Claim ID used to acquire this task"), "text": str("Result, max 32000 bytes; Markdown is rendered, a whole HTML document is displayed as-is in a sandboxed frame. Empty means no post")}, "taskId", "claimId", "text"),
		tool("post_report", "Post a proactive report as Hermes. Use a unique id for each report and the same id on delivery retries. Does not invoke other agents.", false, map[string]any{"id": str("Unique report ID, max 200 characters"), "text": str("Markdown report, max 32000 bytes; a whole HTML document is displayed as-is in a sandboxed frame"), "replyTo": str("Optional existing entry ID")}, "id", "text"),
	}
}

type mcpArgs struct {
	Limit         int    `json:"limit"`
	Before        string `json:"before"`
	EntryID       string `json:"entryId"`
	Persona       string `json:"persona"`
	Path          string `json:"path"`
	Text          string `json:"text"`
	SourceEntryID string `json:"sourceEntryId"`
	TaskID        string `json:"taskId"`
	ClaimID       string `json:"claimId"`
	ID            string `json:"id"`
	ReplyTo       string `json:"replyTo"`
}

func (s *Server) hermesActive() bool {
	for _, id := range s.store.Settings().Active {
		if id == "hermes" {
			return true
		}
	}
	return false
}

func (s *Server) callMCPTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	ctx, cancel := mcpContext(ctx)
	defer cancel()
	if !s.hermesActive() {
		return nil, errors.New("担当画面でHermesを有効にしてください")
	}
	var a mcpArgs
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, errors.New("引数が不正です")
	}
	// Enforce the actual selected tool's schema, not just the union of fields.
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	for _, tool := range kakerattaMCPTools() {
		if tool.Name != name {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]any)
		for key := range fields {
			if _, ok := props[key]; !ok {
				return nil, fmt.Errorf("未知の引数: %s", key)
			}
		}
		for _, key := range tool.InputSchema["required"].([]string) {
			if value, ok := fields[key]; !ok || string(value) == "null" {
				return nil, fmt.Errorf("必須の引数: %s", key)
			}
		}
	}
	switch name {
	case "list_personas":
		rows := []map[string]any{}
		for _, p := range s.catalogue.All() {
			rows = append(rows, map[string]any{"id": p.ID, "name": p.Name, "domain": p.Domain})
		}
		return map[string]any{"personas": rows}, nil
	case "get_timeline":
		if a.Limit == 0 {
			a.Limit = 20
		}
		if a.Limit < 1 || a.Limit > 50 {
			return nil, errors.New("limitは1〜50です")
		}
		return s.store.TimelinePage(ctx, a.Limit, a.Before)
	case "get_thread":
		entries, err := s.mcpThread(ctx, a.EntryID)
		return map[string]any{"entries": entries}, err
	case "list_notes", "read_note":
		if _, ok := s.catalogue.Find(a.Persona); !ok {
			return nil, errors.New("担当が見つかりません")
		}
		if name == "list_notes" {
			notes := []map[string]any{}
			for _, n := range s.store.Memory(a.Persona).Notes {
				notes = append(notes, map[string]any{"path": n.Path, "written": n.Written})
			}
			return map[string]any{"notes": notes}, nil
		}
		note, ok := s.store.Note(a.Persona, strings.TrimPrefix(a.Path, "/"))
		if !ok {
			return nil, errors.New("メモが見つかりません")
		}
		return note, nil
	}
	switch name {
	case "write_note":
		var sources []store.NoteSource
		if a.SourceEntryID != "" {
			entry, ok := s.store.Entry(a.SourceEntryID)
			if !ok || entry.Deleted {
				return nil, errors.New("出典の投稿が見つかりません")
			}
			sources = append(sources, store.NoteSource{EntryID: entry.ID, Label: entry.Text})
		}
		if err := s.store.WriteNote("hermes", strings.TrimPrefix(a.Path, "/"), a.Text, sources...); err != nil {
			return nil, err
		}
		return map[string]bool{"saved": true}, nil
	case "list_tasks":
		tasks, err := store.AgentTasks(ctx, s.store)
		if err != nil {
			return nil, err
		}
		out := []mcpAgentTask{}
		for _, task := range tasks {
			if task.State == "completed" {
				if !task.Published {
					if _, err := s.publishTask(ctx, task); err != nil {
						return nil, err
					}
				}
				continue
			}
			if _, err := s.taskContext(ctx, task); err != nil {
				if !errors.Is(err, errInvalidAgentTask) {
					return nil, err
				}
				if err := store.CancelAgentTask(ctx, s.store, task.ID); err != nil {
					return nil, err
				}
				s.finishHermesTurn(task.Trigger, false)
				continue
			}
			out = append(out, agentTaskResponse(task))
		}
		return map[string]any{"tasks": out}, nil
	case "claim_task":
		if strings.TrimSpace(a.ClaimID) == "" || len(a.ClaimID) > 200 {
			return nil, errors.New("claimIdは1〜200文字です")
		}
		tasks, err := store.AgentTasks(ctx, s.store)
		if err != nil {
			return nil, err
		}
		var task *store.AgentTask
		for i := range tasks {
			if tasks[i].ID == a.TaskID {
				task = &tasks[i]
				break
			}
		}
		if task == nil {
			return nil, errors.New("依頼が見つかりません")
		}
		thread, err := s.taskContext(ctx, *task)
		if err != nil {
			return nil, err
		}
		claimed, err := store.ClaimAgentTask(ctx, s.store, a.TaskID, a.ClaimID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": agentTaskResponse(claimed), "thread": thread, "instructions": s.hermesInstructions()}, nil
	case "cancel_task":
		if a.TaskID == "" {
			return nil, errors.New("taskIdを指定してください")
		}
		tasks, err := store.AgentTasks(ctx, s.store)
		if err != nil {
			return nil, err
		}
		var trigger string
		for _, task := range tasks {
			if task.ID == a.TaskID {
				trigger = task.Trigger
				break
			}
		}
		if err := store.CancelAgentTask(ctx, s.store, a.TaskID); err != nil {
			return nil, err
		}
		s.finishHermesTurn(trigger, false)
		return map[string]bool{"cancelled": true}, nil
	case "complete_task":
		if a.ClaimID == "" || len(a.Text) > 32000 {
			return nil, errors.New("claimIdを指定し、本文は32KB以内にしてください")
		}
		task, err := store.FinishAgentTask(ctx, s.store, a.TaskID, a.ClaimID, strings.TrimSpace(a.Text))
		if err != nil {
			return nil, err
		}
		return s.publishTask(ctx, task)
	case "post_report":
		if strings.TrimSpace(a.ID) == "" || len(a.ID) > 200 || strings.TrimSpace(a.Text) == "" || len(a.Text) > 32000 {
			return nil, errors.New("idと本文を指定してください（idは200文字、本文は32KBまで）")
		}
		return s.publishMCPEntry("report:"+a.ID, a.Text, a.ReplyTo, time.Now())
	}
	return nil, errors.New("Unknown tool")
}

// finishHermesTurn tells the routing sheet Hermes's out-of-process turn has
// actually concluded. The runner is absent in tests that exercise MCP tool
// logic in isolation, so this is a no-op there rather than a nil dereference.
func (s *Server) finishHermesTurn(entryID string, spoke bool) {
	if s.runner != nil {
		s.runner.FinishAsyncTurn(entryID, "hermes", spoke)
	}
}

func (s *Server) hermesInstructions() string {
	skill, _ := s.catalogue.Find("hermes")
	return skill.Tone + "\n" + skill.Instructions + "\nUse your own tools and skills. For check-ins, report only meaningful changes; complete quietly otherwise. Store shared notes using write_note with Markdown links relative to the Hermes bundle."
}

// Only confirmed deletion or editing invalidates a task. Storage failures must
// leave its claim and delivery state intact so the next request can retry.
var errInvalidAgentTask = errors.New("依頼の投稿は削除または編集されています。新しい依頼を確認してください")

func (s *Server) mcpThread(ctx context.Context, id string) ([]timeline.Entry, error) {
	return store.SearchThread(ctx, s.store, id)
}

func (s *Server) taskContext(ctx context.Context, task store.AgentTask) ([]timeline.Entry, error) {
	if task.Trigger == "" {
		return []timeline.Entry{}, nil
	}
	thread, err := s.mcpThread(ctx, task.Trigger)
	if errors.Is(err, store.ErrSearchThreadMissing) {
		return nil, errInvalidAgentTask
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range thread {
		if entry.ID == task.Trigger && !entry.Deleted {
			if fmt.Sprintf("%x", sha256.Sum256([]byte(entry.Text))) != task.Fingerprint {
				return nil, errInvalidAgentTask
			}
			return thread, nil
		}
	}
	return nil, errInvalidAgentTask
}

func (s *Server) publishTask(ctx context.Context, task store.AgentTask) (any, error) {
	var result any = map[string]any{"completed": true, "posted": false}
	_, err := s.taskContext(ctx, task)
	if err != nil && !errors.Is(err, errInvalidAgentTask) {
		return nil, err
	}
	spoke := false
	if err == nil && task.Result != "" {
		result, err = s.publishMCPEntry("task:"+task.ID, task.Result, task.Trigger, time.Now())
		if err != nil {
			return nil, err
		}
		spoke = true
	}
	if err := store.MarkAgentTaskPublished(ctx, s.store, task.ID); err != nil {
		return nil, err
	}
	// The routing sheet has been saying Hermes is still thinking since this
	// task was queued; now it actually knows the outcome.
	s.finishHermesTurn(task.Trigger, spoke)
	return result, nil
}

func (s *Server) publishMCPEntry(key, text, replyTo string, at time.Time) (any, error) {
	id := fmt.Sprintf("mcp_%x", sha256.Sum256([]byte(key)))
	if existing, ok := s.store.Entry(id); ok {
		return map[string]any{"id": existing.ID, "created": false}, nil
	}
	entry := timeline.Entry{ID: id, Kind: timeline.KindPost, Speaker: "hermes", Text: strings.TrimSpace(text), At: at, Root: id}
	if replyTo != "" {
		parent, ok := s.store.Entry(replyTo)
		if !ok || parent.Deleted {
			return nil, errors.New("返信先が見つかりません")
		}
		root, ok := s.store.Entry(timeline.RootOf(parent))
		if !ok || root.Deleted {
			return nil, errors.New("元の投稿が見つかりません")
		}
		entry.Kind = timeline.KindRemark
		entry.Root = root.ID
		entry.ReplyTo = parent.ID
	}
	saved, created, err := s.store.CreateEntry(entry)
	if err != nil {
		return nil, err
	}
	if created {
		s.publishEvent("entry", saved)
	}
	return map[string]any{"id": saved.ID, "created": created}, nil
}
