package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"regexp"
	"time"
)

var jobName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func (g *Gateway) passive() bool { return g.options.Mode == "web" || g.options.Mode == "worker" }

func (g *Gateway) serveJob(w http.ResponseWriter, r *http.Request) {
	if g.options.Mode != "worker" || g.options.VerifyJob == nil || g.shared == nil {
		notFound(w, r)
		return
	}
	job, err := g.options.VerifyJob(r)
	if err != nil {
		fail(w, http.StatusUnauthorized, "invalid push credential or envelope")
		return
	}
	key, err := hex.DecodeString(job.Workspace)
	if err != nil || len(key) != sha256.Size || !jobName.MatchString(job.Reaction) || !jobName.MatchString(job.Dispatch) {
		fail(w, http.StatusBadRequest, "invalid job")
		return
	}
	// Admission shares the shutdown mutex, so Wait cannot race Add.
	g.mu.Lock()
	select {
	case <-g.stop:
		g.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "worker stopping")
		return
	default:
	}
	g.opening.Add(1)
	g.mu.Unlock()
	defer g.opening.Done()
	select {
	case g.jobSlot <- struct{}{}:
		defer func() { <-g.jobSlot }()
	default:
		fail(w, http.StatusServiceUnavailable, "worker busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 480*time.Second)
	defer cancel()
	exists, err := g.shared.Exists(ctx, job.Workspace)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "workspace check failed")
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	closing, err := g.shared.Closing(ctx, job.Workspace)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "closing check failed")
		return
	}
	if closing {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s, err := g.open(job.Workspace)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "workspace open failed")
		return
	}
	defer closeWorkspace(s)
	defer s.Halt()
	if err := s.runner.WorkJob(ctx, job); err != nil {
		log.Printf("worker job retry: %v", err)
		fail(w, http.StatusServiceUnavailable, "job pending; retry")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) serveDispatchRepair(w http.ResponseWriter, r *http.Request) {
	if g.options.Mode != "web" || g.options.VerifyRepair == nil || g.options.RepairDispatch == nil {
		notFound(w, r)
		return
	}
	if err := g.options.VerifyRepair(r); err != nil {
		fail(w, http.StatusUnauthorized, "invalid repair credential")
		return
	}
	g.mu.Lock()
	select {
	case <-g.stop:
		g.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "server stopping")
		return
	default:
	}
	g.opening.Add(1)
	g.mu.Unlock()
	defer g.opening.Done()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := g.options.RepairDispatch(ctx); err != nil {
		fail(w, http.StatusServiceUnavailable, "dispatch retry required")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
