// Package hub connects to jcode and fans state out to web clients.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elee1766/jcode-web/internal/jcode"
	"github.com/elee1766/jcode-web/internal/store"
	"github.com/elee1766/jcode-web/internal/summarize"
)

type Config struct {
	MainSocket   string
	DebugSocket  string
	PollInterval time.Duration
	Retention    time.Duration
	// Attachment idles this long with no web viewers before it closes.
	AttachLinger time.Duration
}

// Notifier receives session transitions worth alerting on.
type Notifier interface {
	Notify(ctx context.Context, title, body, tag, url string)
}

type Hub struct {
	cfg    Config
	log    *slog.Logger
	store  *store.Store
	debug  *jcode.Debug
	notify Notifier

	mu              sync.Mutex
	eventMu         sync.Mutex
	sessions        map[string]jcode.SessionInfo
	lastEvent       map[string]int64
	scheduledCounts map[string]uint32
	connected       bool
	connErr         string
	watchers        map[chan WatchUpdate]struct{}
	attached        map[string]*attachment

	sum       *summarize.Client
	sumQueue  chan summaryRequest
	sumQueued map[string]time.Time
	sumGate   chan struct{}
	summaries map[string]store.Summary
}

// WatchUpdate is one item for Watch subscribers. Exactly one field is set.
type WatchUpdate struct {
	Event      *store.Event
	Sessions   []SessionView
	Connection *ConnectionState
}

type ConnectionState struct {
	Connected bool
	Error     string
}

type SessionView struct {
	jcode.SessionInfo
	LastEventUnix      int64
	Attached           bool
	Summary            string
	ShortSummary       string
	SummaryAt          int64
	ScheduledTaskCount uint32
}

func New(cfg Config, log *slog.Logger, st *store.Store, notify Notifier) *Hub {
	return &Hub{
		cfg:       cfg,
		log:       log,
		store:     st,
		debug:     &jcode.Debug{Path: cfg.DebugSocket},
		notify:    notify,
		sessions:  map[string]jcode.SessionInfo{},
		lastEvent: map[string]int64{},
		watchers:  map[chan WatchUpdate]struct{}{},
		attached:  map[string]*attachment{},
		summaries: map[string]store.Summary{},
	}
}

func (h *Hub) Debug() *jcode.Debug { return h.debug }

// Run blocks until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	if times, err := h.store.LastEventTimes(ctx); err == nil {
		h.mu.Lock()
		h.lastEvent = times
		h.mu.Unlock()
	}
	h.loadSummaries(ctx)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); h.pollLoop(ctx) }()
	go func() { defer wg.Done(); h.busLoop(ctx) }()
	go func() { defer wg.Done(); h.pruneLoop(ctx) }()
	go func() { defer wg.Done(); h.summaryLoop(ctx) }()
	wg.Wait()
}

func (h *Hub) pollLoop(ctx context.Context) {
	t := time.NewTicker(h.cfg.PollInterval)
	defer t.Stop()
	for {
		h.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refresh polls the session list and broadcasts it when it changed.
func (h *Hub) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := h.debug.Sessions(ctx)
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) {
			h.setConnection(false, err.Error())
		}
		return
	}
	h.setConnection(true, "")

	counts := map[string]uint32{}
	queue, queueErr := h.debug.ScheduledQueue(ctx)
	if queueErr == nil {
		for _, task := range queue {
			id := task.TargetSessionID
			if task.TargetKind == "spawn" {
				id = task.TargetParentSessionID
			} else if task.TargetKind != "session" {
				continue
			}
			if id != "" {
				counts[id]++
			}
		}
	}

	next := make(map[string]jcode.SessionInfo, len(list))
	for _, s := range list {
		next[s.SessionID] = s
	}
	h.mu.Lock()
	prev := h.sessions
	h.sessions = next
	changed := len(prev) != len(next)
	if queueErr == nil {
		changed = changed || !maps.Equal(h.scheduledCounts, counts)
		h.scheduledCounts = counts
	}
	for id, s := range next {
		p, ok := prev[id]
		if !ok || !sameSession(p, s) {
			changed = true
		}
	}
	h.mu.Unlock()
	if !changed {
		return
	}
	h.broadcast(WatchUpdate{Sessions: h.Sessions()})
}

func sameSession(a, b jcode.SessionInfo) bool {
	ad, bd := "", ""
	if a.Detail != nil {
		ad = *a.Detail
	}
	if b.Detail != nil {
		bd = *b.Detail
	}
	return a.Status == b.Status && a.IsProcessing == b.IsProcessing && a.FriendlyName == b.FriendlyName &&
		a.Model == b.Model && ad == bd && a.TokenUsage.Input == b.TokenUsage.Input && a.TokenUsage.Output == b.TokenUsage.Output &&
		a.ParentID == b.ParentID && a.IsHeadless == b.IsHeadless && a.WorkingDir == b.WorkingDir && a.SwarmID == b.SwarmID
}

func (h *Hub) setConnection(ok bool, msg string) {
	h.mu.Lock()
	changed := h.connected != ok || h.connErr != msg
	h.connected, h.connErr = ok, msg
	h.mu.Unlock()
	if changed {
		if !ok {
			h.log.Warn("jcode unreachable", "error", msg)
		} else {
			h.log.Info("jcode connected")
		}
		h.broadcast(WatchUpdate{Connection: &ConnectionState{Connected: ok, Error: msg}})
	}
}

// Sessions returns the current list sorted by most recent activity.
func (h *Hub) Sessions() []SessionView {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]SessionView, 0, len(h.sessions))
	for id, s := range h.sessions {
		_, att := h.attached[id]
		sm := h.summaries[id]
		out = append(out, SessionView{SessionInfo: s, LastEventUnix: max(h.lastEvent[id], s.LastActivityUnix), Attached: att, Summary: sm.Text, ShortSummary: sm.Title, SummaryAt: sm.UpdatedAt, ScheduledTaskCount: h.scheduledCounts[id]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsProcessing != out[j].IsProcessing {
			return out[i].IsProcessing
		}
		if out[i].LastEventUnix != out[j].LastEventUnix {
			return out[i].LastEventUnix > out[j].LastEventUnix
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

func (h *Hub) Connection() ConnectionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return ConnectionState{Connected: h.connected, Error: h.connErr}
}

func (h *Hub) session(id string) (jcode.SessionInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	return s, ok
}

func (h *Hub) busLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := h.debug.Subscribe(ctx, func(ev jcode.BusEvent) { h.ingest(ctx, ev) })
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		h.log.Warn("event bus disconnected", "error", err, "retry", backoff)
		if errors.Is(err, jcode.ErrDebugDisabled) {
			h.setConnection(false, err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (h *Hub) ingest(ctx context.Context, ev jcode.BusEvent) {
	if ev.Type == "lag" {
		h.log.Warn("event bus lagged", "missed", ev.Missed)
		return
	}
	typ := ev.EventType()
	e := &store.Event{
		UpstreamID:    ev.ID,
		SessionID:     ev.SessionID,
		SessionName:   ev.SessionName,
		SwarmID:       ev.SwarmID,
		TimestampUnix: ev.TimestampUnix,
		Type:          typ,
		PayloadJSON:   string(ev.Event),
		Summary:       Summarize(typ, ev.Event),
	}
	if e.TimestampUnix == 0 {
		e.TimestampUnix = time.Now().Unix()
	}
	if err := h.publishEvent(ctx, e); err != nil {
		h.log.Error("store event", "error", err)
		return
	}

	if typ == "status_change" || typ == "member_change" {
		go h.refresh(ctx)
	}
	if typ == "status_change" {
		var p struct {
			Old string `json:"old_status"`
			New string `json:"new_status"`
		}
		if json.Unmarshal(ev.Event, &p) == nil && p.Old == "running" {
			h.resync(e.SessionID)
			reason := "turn_complete"
			if p.New == "failed" || p.New == "blocked" {
				reason = "needs_attention"
			}
			if p.New == "ready" || p.New == "completed" || p.New == "failed" || p.New == "blocked" {
				h.queueSummary(e.SessionID, reason)
			}
		}
	} else if typ == "plan_update" {
		h.queueSummary(e.SessionID, "plan_updated")
	} else if typ == "file_touch" {
		var p struct {
			Op string `json:"op"`
		}
		if json.Unmarshal(ev.Event, &p) == nil && p.Op != "read" && p.Op != "" {
			h.queueSummary(e.SessionID, "progress")
		}
	}
	h.maybeNotify(ctx, e)
}

func (h *Hub) publishEvent(ctx context.Context, e *store.Event) error {
	h.eventMu.Lock()
	defer h.eventMu.Unlock()
	if err := h.store.InsertEvent(ctx, e); err != nil {
		return err
	}
	if e.Type != "session_summary" {
		h.mu.Lock()
		h.lastEvent[e.SessionID] = e.TimestampUnix
		h.mu.Unlock()
	}
	h.broadcast(WatchUpdate{Event: e})
	return nil
}

func (h *Hub) maybeNotify(ctx context.Context, e *store.Event) {
	if h.notify == nil || e.Type != "status_change" {
		return
	}
	var p struct {
		Old string `json:"old_status"`
		New string `json:"new_status"`
	}
	if json.Unmarshal([]byte(e.PayloadJSON), &p) != nil {
		return
	}
	name := e.SessionName
	if name == "" {
		name = e.SessionID
	}
	if s, ok := h.session(e.SessionID); ok && s.WorkingDir != "" {
		name = fmt.Sprintf("%s (%s)", name, shortDir(s.WorkingDir))
	}
	var body string
	switch {
	case p.Old == "running" && (p.New == "ready" || p.New == "completed"):
		body = "finished"
	case p.New == "failed":
		body = "failed"
	case p.New == "blocked":
		body = "needs input"
	default:
		return
	}
	if h.sum.Enabled() && p.Old == "running" {
		// The summary worker sends the notification with the milestone text.
		return
	}
	h.notify.Notify(ctx, name, body, e.SessionID, "/#/s/"+e.SessionID)
}

func shortDir(dir string) string {
	parts := strings.Split(strings.TrimRight(dir, "/"), "/")
	if len(parts) <= 2 {
		return dir
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

func (h *Hub) pruneLoop(ctx context.Context) {
	if h.cfg.Retention <= 0 {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := h.store.PruneEvents(ctx, h.cfg.Retention); err != nil {
			h.log.Error("prune events", "error", err)
		} else if n > 0 {
			h.log.Info("pruned events", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Watch registers a subscriber. Call the returned func to unsubscribe.
func (h *Hub) Watch() (<-chan WatchUpdate, func()) {
	ch := make(chan WatchUpdate, 256)
	h.mu.Lock()
	h.watchers[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.watchers, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) broadcast(u WatchUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.watchers {
		select {
		case ch <- u:
		default:
			delete(h.watchers, ch)
			close(ch)
		}
	}
}
