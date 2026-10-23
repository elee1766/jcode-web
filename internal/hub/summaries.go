package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elee1766/jcode-web/internal/jcode"
	"github.com/elee1766/jcode-web/internal/store"
	"github.com/elee1766/jcode-web/internal/summarize"
)

// SetSummarizer enables AI summaries. Call before Run.
func (h *Hub) SetSummarizer(s *summarize.Client) {
	h.sum = s
	h.sumQueue = make(chan summaryRequest, 256)
	h.sumQueued = map[string]time.Time{}
	h.sumGate = make(chan struct{}, 1)
}

func (h *Hub) SummariesEnabled() bool { return h.sum.Enabled() }

func (h *Hub) SummaryModel() string {
	if !h.sum.Enabled() {
		return ""
	}
	return h.sum.Model()
}

func (h *Hub) loadSummaries(ctx context.Context) {
	m, err := h.store.Summaries(ctx)
	if err != nil {
		h.log.Error("load summaries", "error", err)
		return
	}
	h.mu.Lock()
	h.summaries = m
	h.mu.Unlock()
}

// queueSummary schedules a refresh. Duplicates collapse in the worker.
type summaryRequest struct {
	ID, Reason string
	At         time.Time
}

func (h *Hub) queueSummary(sessionID, reason string) {
	if !h.sum.Enabled() {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if (reason == "progress" || reason == "plan_updated") && time.Since(h.sumQueued[sessionID]) < 45*time.Second {
		return
	}
	select {
	case h.sumQueue <- summaryRequest{ID: sessionID, Reason: reason, At: time.Now().Add(3 * time.Second)}:
		h.sumQueued[sessionID] = time.Now()
	default:
	}
}

func (h *Hub) summaryLoop(ctx context.Context) {
	if !h.sum.Enabled() {
		return
	}
	h.log.Info("summaries enabled", "model", h.sum.Model())
	pending := map[string]summaryRequest{}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-h.sumQueue:
			old, ok := pending[req.ID]
			if !ok || req.Reason == "turn_complete" || req.Reason == "needs_attention" || (old.Reason != "turn_complete" && old.Reason != "needs_attention") {
				pending[req.ID] = req
			}
		case now := <-tick.C:
			for id, req := range pending {
				if now.Before(req.At) {
					continue
				}
				delete(pending, id)
				sum, err := h.summarizeSession(ctx, id, req.Reason)
				if err != nil {
					h.log.Warn("summarize session", "session", id, "error", err)
				}
				if h.notify != nil && (req.Reason == "turn_complete" || req.Reason == "needs_attention") {
					info, _ := h.session(id)
					body := "finished"
					if req.Reason == "needs_attention" {
						body = "needs attention"
					}
					if err == nil {
						body += ": " + sum.Text
					}
					h.notify.Notify(ctx, info.FriendlyName, body, id, "/#/s/"+id)
				}
			}
		}
	}
}

// SummarizeSession regenerates and stores the summary for one session.
func (h *Hub) SummarizeSession(ctx context.Context, id string) (store.Summary, error) {
	return h.summarizeSession(ctx, id, "manual")
}

func (h *Hub) summarizeSession(ctx context.Context, id, reason string) (store.Summary, error) {
	select {
	case h.sumGate <- struct{}{}:
		defer func() { <-h.sumGate }()
	case <-ctx.Done():
		return store.Summary{}, ctx.Err()
	}
	info, _ := h.session(id)
	h.mu.Lock()
	previous := h.summaries[id]
	h.mu.Unlock()
	var report summarize.Report
	var err error
	if reason != "manual" {
		events, e := h.store.EventsBefore(ctx, id, 0, 60)
		if e != nil {
			return store.Summary{}, e
		}
		var lines []string
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			if ev.Type != "session_summary" && ev.TimestampUnix >= previous.UpdatedAt {
				lines = append(lines, ev.Summary)
			}
		}
		report, err = h.sum.Milestone(ctx, info.FriendlyName, info.WorkingDir, reason, previous.Text, lines)
	} else {
		hist, err := h.historyFor(ctx, id)
		if err != nil {
			return store.Summary{}, err
		}
		if len(hist) == 0 {
			return store.Summary{}, fmt.Errorf("empty history")
		}
		turns := make([]summarize.Turn, 0, len(hist))
		for _, m := range hist {
			t := summarize.Turn{Role: m.Role, Content: m.Content}
			if m.ToolData != nil {
				t.Tool, t.Intent = m.ToolData.Name, m.ToolData.Intent
			}
			turns = append(turns, t)
		}
		report, err = h.sum.Session(ctx, info.FriendlyName, info.WorkingDir, turns)
		if err != nil {
			return store.Summary{}, err
		}
	}
	if err != nil {
		return store.Summary{}, err
	}
	if strings.TrimSpace(report.Summary) == "" {
		return store.Summary{}, fmt.Errorf("empty summary response")
	}
	s := store.Summary{SessionID: id, Title: report.Title, Text: report.Summary, Model: h.sum.Model(), UpdatedAt: time.Now().Unix()}
	if err := h.store.PutSummary(ctx, s); err != nil {
		return s, err
	}
	h.mu.Lock()
	h.summaries[id] = s
	h.mu.Unlock()
	payload, _ := json.Marshal(map[string]string{"reason": reason, "model": s.Model, "working_dir": info.WorkingDir, "title": s.Title})
	event := &store.Event{SessionID: id, SessionName: info.FriendlyName, SwarmID: info.SwarmID, TimestampUnix: s.UpdatedAt, Type: "session_summary", Summary: s.Text, PayloadJSON: string(payload)}
	if err := h.publishEvent(ctx, event); err != nil {
		return s, err
	}
	h.broadcast(WatchUpdate{Sessions: h.Sessions()})
	return s, nil
}

// historyFor returns a session's history. It uses the cached attachment when
// one exists. Otherwise it subscribes briefly, which unlike the debug
// history command does not wait for a running turn.
func (h *Hub) historyFor(ctx context.Context, id string) ([]jcode.HistoryMessage, error) {
	h.mu.Lock()
	a := h.attached[id]
	h.mu.Unlock()
	if a != nil {
		select {
		case <-a.ready:
			a.mu.Lock()
			hist := append([]jcode.HistoryMessage(nil), a.history...)
			a.mu.Unlock()
			if a.err == nil && len(hist) > 0 {
				return hist, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := jcode.Attach(actx, h.cfg.MainSocket, id)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	for {
		select {
		case env, ok := <-s.Events():
			if !ok {
				return nil, s.Err()
			}
			if env.Type != "history" {
				continue
			}
			var hist jcode.History
			if err := json.Unmarshal(env.Raw, &hist); err != nil {
				return nil, fmt.Errorf("decode history: %w", err)
			}
			return hist.Messages, nil
		case <-actx.Done():
			return nil, actx.Err()
		}
	}
}

// Overview summarizes recent activity across sessions active within window.
func (h *Hub) Overview(ctx context.Context, window time.Duration) (string, error) {
	if !h.sum.Enabled() {
		return "", fmt.Errorf("summaries are not configured")
	}
	since := time.Now().Add(-window).Unix()
	evs, err := h.store.EventsBefore(ctx, "", 0, 400)
	if err != nil {
		return "", err
	}
	bySession := map[string][]string{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.TimestampUnix < since {
			continue
		}
		bySession[e.SessionID] = append(bySession[e.SessionID], e.Summary)
	}
	views := h.Sessions()
	var items []summarize.OverviewItem
	for _, v := range views {
		ev := bySession[v.SessionID]
		if len(ev) == 0 && !v.IsProcessing {
			continue
		}
		if len(ev) > 12 {
			ev = ev[len(ev)-12:]
		}
		detail := ""
		if v.Detail != nil {
			detail = *v.Detail
		}
		items = append(items, summarize.OverviewItem{
			Name: v.FriendlyName, Dir: v.WorkingDir, Status: v.Status, Detail: detail,
			Summary: v.Summary, Events: ev,
		})
	}
	if len(items) == 0 {
		return "No session activity in this window.", nil
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Status == "failed" && items[j].Status != "failed" })
	if len(items) > 16 {
		items = items[:16]
	}
	return h.sum.Overview(ctx, items)
}
