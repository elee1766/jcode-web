package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/elee1766/jcode-web/internal/jcode"
	"github.com/elee1766/jcode-web/internal/store"
	"github.com/elee1766/jcode-web/internal/summarize"
)

// SetSummarizer enables AI summaries. Call before Run.
func (h *Hub) SetSummarizer(s *summarize.Client) {
	h.sum = s
	h.sumQueue = make(chan string, 256)
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
func (h *Hub) queueSummary(sessionID string) {
	if !h.sum.Enabled() {
		return
	}
	select {
	case h.sumQueue <- sessionID:
	default:
	}
}

func (h *Hub) summaryLoop(ctx context.Context) {
	if !h.sum.Enabled() {
		return
	}
	h.log.Info("summaries enabled", "model", h.sum.Model())
	pending := map[string]time.Time{}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-h.sumQueue:
			if _, ok := pending[id]; !ok {
				// Debounce bursts of turn endings.
				pending[id] = time.Now().Add(3 * time.Second)
			}
		case now := <-tick.C:
			for id, at := range pending {
				if now.Before(at) {
					continue
				}
				delete(pending, id)
				h.mu.Lock()
				recent := time.Now().Unix()-h.summaries[id].UpdatedAt < 10
				h.mu.Unlock()
				if recent {
					continue
				}
				if _, err := h.SummarizeSession(ctx, id); err != nil {
					h.log.Warn("summarize session", "session", id, "error", err)
				}
			}
		}
	}
}

// SummarizeSession regenerates and stores the summary for one session.
func (h *Hub) SummarizeSession(ctx context.Context, id string) (store.Summary, error) {
	info, _ := h.session(id)
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
	text, err := h.sum.Session(ctx, info.FriendlyName, info.WorkingDir, turns)
	if err != nil {
		return store.Summary{}, err
	}
	s := store.Summary{SessionID: id, Text: text, Model: h.sum.Model(), UpdatedAt: time.Now().Unix()}
	if err := h.store.PutSummary(ctx, s); err != nil {
		return s, err
	}
	h.mu.Lock()
	h.summaries[id] = s
	h.mu.Unlock()
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
