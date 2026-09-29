package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/takeshy/kakeratta/internal/persona"
	"github.com/takeshy/kakeratta/internal/store"
	"github.com/takeshy/kakeratta/internal/tools"
)

func (s *Server) getPersonas(w http.ResponseWriter, r *http.Request) {
	settings := s.store.Settings()
	skills := s.catalogue.All()
	type row struct {
		persona.Skill
		DailyCheckIn store.DailyCheckIn `json:"dailyCheckIn"`
		// Schedules are the wakes this persona asked the app for, so the card can
		// show what it is waiting for.
		Schedules []store.Schedule `json:"schedules,omitempty"`
		Active    bool             `json:"active"`
		Wakes     int              `json:"wakes"`
		Notes     string           `json:"notes,omitempty"`
		// Planner is "builtin" or "external": who reads the context and
		// writes this persona's workflow.
		Planner string `json:"planner"`
	}
	active := map[string]bool{}
	for _, id := range settings.Active {
		active[id] = true
	}
	out := make([]row, 0, len(skills))
	mcp, _ := s.mcpConnections(r.Context())
	for _, skill := range skills {
		memory := s.store.MemoryMeta(skill.ID)
		schedules, err := s.store.Schedules(skill.ID)
		if err != nil {
			// A listing failure must not hide the room; the card shows none.
			schedules = nil
		}
		planner := "builtin"
		// The saved choice, paused or not: the card keeps showing it.
		if settings.Planners[skill.ID] == store.PlannerExternal {
			planner = store.PlannerExternal
		}
		checkIn := settings.CheckInFor(skill.ID)
		if checkIn.Every == store.CheckInMonthly {
			checkIn.On = store.SummaryDay(s.store.Root())
		}
		out = append(out, row{Skill: skill, DailyCheckIn: checkIn, Schedules: schedules, Active: active[skill.ID], Wakes: memory.Wakes, Planner: planner})
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": out, "tools": tools.New(s.tools).Names(), "mcpConfigured": len(mcp) > 0, "externalAgentPaused": settings.ExternalAgent.Paused, "externalAgentAway": s.agentStatus()["away"]})
}

func (s *Server) putPersonas(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active []string `json:"active"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	settings := s.store.ConfiguredSettings()
	previouslyActive := make(map[string]bool, len(settings.Active))
	for _, id := range settings.Active {
		previouslyActive[id] = true
	}
	var kept []string
	stillActive := make(map[string]bool, len(body.Active))
	for _, id := range body.Active {
		if _, ok := s.catalogue.Find(id); ok {
			kept = append(kept, id)
			stillActive[id] = true
		}
	}
	settings.Active = kept
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Wakes belong to an active persona. Remove them at the moment it is
	// paused, including future schedules that the minute loop has not reached.
	for id := range previouslyActive {
		if !stillActive[id] {
			if err := s.store.DeleteSchedules(id); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	s.getPersonas(w, r)
}

func (s *Server) installPersona(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "読めませんでした")
		return
	}
	skill, err := s.catalogue.Install(raw, s.knownTool)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	encoded, err := json.Marshal(skill)
	if err != nil || s.store.PersistSkill(skill.ID, encoded) != nil {
		_ = s.catalogue.Uninstall(skill.ID)
		fail(w, http.StatusInternalServerError, "担当を保存できませんでした")
		return
	}
	writeJSON(w, http.StatusOK, added{Skill: skill, Active: s.activatePersona(skill.ID) == nil})
}

// added is a new persona as the one who made it sees it: whether it is already
// in the room decides what the confirmation says.
type added struct {
	persona.Skill
	Active bool `json:"active"`
}

// activatePersona puts a newly added persona in the room. Adding one means
// wanting to use it, and a separate switch nobody expects left new personas
// silent; pausing one stays a switch on its card.
func (s *Server) activatePersona(id string) error {
	settings := s.store.ConfiguredSettings()
	if slices.Contains(settings.Active, id) {
		return nil
	}
	settings.Active = append(settings.Active, id)
	return s.store.SaveSettings(settings)
}

func (s *Server) uninstallPersona(w http.ResponseWriter, r *http.Request) {
	if err := s.catalogue.Uninstall(r.PathValue("id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DeleteSkill(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A removed persona has no panel left to show or forget its notes from, so
	// its memory goes with it rather than lingering where nobody can reach it.
	if err := s.store.ForgetMemory(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.DeleteSchedules(r.PathValue("id")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deleteSchedule takes one wake a persona asked for off its card. The persona
// sets them, but the person it would wake is the one who says when it stops;
// the persona is not woken about it.
func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteSchedule(r.PathValue("id"), r.PathValue("schedule"))
	if errors.Is(err, store.ErrScheduleNotFound) {
		fail(w, http.StatusNotFound, "予定が見つかりません")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.getPersonas(w, r)
}

// putDailyCheckIn chooses how much a persona says at its daily check-in.
// When it runs is the app's to choose, so only Talk is kept.
func (s *Server) putDailyCheckIn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.catalogue.Find(id); !ok {
		fail(w, http.StatusNotFound, "担当が見つかりません")
		return
	}
	var body store.DailyCheckIn
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "設定を読めませんでした")
		return
	}
	switch body.Talk {
	case "":
		body.Talk = store.CheckInShare
	case store.CheckInMinimal, store.CheckInShare:
	default:
		fail(w, http.StatusBadRequest, "話し方が不正です")
		return
	}
	// A subject checks in every day; a person may be heard about monthly.
	switch body.Every {
	case "":
	case store.CheckInMonthly:
		if skill, _ := s.catalogue.Find(id); !skill.IsPerson() {
			fail(w, http.StatusBadRequest, "月1回のチェックインは人物の担当だけです")
			return
		}
	default:
		fail(w, http.StatusBadRequest, "チェックインの頻度が不正です")
		return
	}
	settings := s.store.ConfiguredSettings()
	if settings.DailyCheckIns == nil {
		settings.DailyCheckIns = map[string]store.DailyCheckIn{}
	}
	settings.DailyCheckIns[id] = store.DailyCheckIn{Enabled: true, Talk: body.Talk, Every: body.Every}
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.getPersonas(w, r)
}

// putPlanner chooses who plans a persona's turns: the in-process model, or
// the external agent connected over MCP.
func (s *Server) putPlanner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.catalogue.Find(id); !ok {
		fail(w, http.StatusNotFound, "担当が見つかりません")
		return
	}
	var body struct {
		Planner string `json:"planner"`
	}
	if err := readJSON(r, &body); err != nil || (body.Planner != "builtin" && body.Planner != store.PlannerExternal) {
		fail(w, http.StatusBadRequest, "設定を読めませんでした")
		return
	}
	settings := s.store.ConfiguredSettings()
	if settings.Planners == nil {
		settings.Planners = map[string]string{}
	}
	if body.Planner == store.PlannerExternal {
		settings.Planners[id] = store.PlannerExternal
	} else {
		delete(settings.Planners, id)
	}
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.getPersonas(w, r)
}
