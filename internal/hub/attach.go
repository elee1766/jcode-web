package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/elee1766/jcode-web/internal/jcode"
)

// attachment is one upstream main-socket connection shared by all web
// viewers of a session. It keeps the last history snapshot plus events since,
// so a new viewer can catch up without another subscribe.
type attachment struct {
	h  *Hub
	id string

	mu      sync.Mutex
	sess    *jcode.Session
	history []jcode.HistoryMessage
	// Events since the history snapshot, in order.
	tail    []jcode.Envelope
	viewers map[chan jcode.Envelope]struct{}
	ready   chan struct{}
	err     error
	idle    *time.Timer
}

// maxTail bounds buffered live events. Older ones are dropped and the next
// turn boundary resyncs history.
const maxTail = 20000

// echo records a message sent from the web. jcode only echoes user turns to
// other attachments, so viewers of this one would not see it otherwise.
func (a *attachment) echo(content, source string) {
	b, _ := json.Marshal(map[string]string{"type": "web_user_message", "content": content, "source": source})
	a.dispatch(jcode.Envelope{Type: "web_user_message", Raw: b})
}

// resync refreshes the history snapshot. Called when a turn ends so the tail
// does not grow without bound.
func (h *Hub) resync(sessionID string) {
	h.mu.Lock()
	a := h.attached[sessionID]
	h.mu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		go s.RequestHistory(context.Background())
	}
}

// Viewer is a live subscription to one session.
type Viewer struct {
	History []jcode.HistoryMessage
	Tail    []jcode.Envelope
	Events  <-chan jcode.Envelope
	Close   func()
}

// View attaches to sessionID, connecting upstream if needed.
func (h *Hub) View(ctx context.Context, sessionID string) (*Viewer, error) {
	a := h.attachmentFor(sessionID)
	select {
	case <-a.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if a.err != nil {
		h.dropAttachment(a)
		return nil, a.err
	}
	ch := make(chan jcode.Envelope, 4096)
	a.mu.Lock()
	if a.sess == nil {
		a.mu.Unlock()
		return nil, errors.New("session detached: reconnect")
	}
	if a.idle != nil {
		a.idle.Stop()
		a.idle = nil
	}
	v := &Viewer{
		History: append([]jcode.HistoryMessage(nil), a.history...),
		Tail:    append([]jcode.Envelope(nil), a.tail...),
		Events:  ch,
	}
	a.viewers[ch] = struct{}{}
	a.mu.Unlock()
	v.Close = func() { a.removeViewer(ch) }
	return v, nil
}

func (h *Hub) attachmentFor(id string) *attachment {
	h.mu.Lock()
	a, ok := h.attached[id]
	if !ok {
		a = &attachment{h: h, id: id, viewers: map[chan jcode.Envelope]struct{}{}, ready: make(chan struct{})}
		h.attached[id] = a
	}
	h.mu.Unlock()
	if !ok {
		go a.run()
		go h.broadcast(WatchUpdate{Sessions: h.Sessions()})
	}
	return a
}

func (h *Hub) dropAttachment(a *attachment) {
	h.mu.Lock()
	if h.attached[a.id] == a {
		delete(h.attached, a.id)
	}
	h.mu.Unlock()
	go h.broadcast(WatchUpdate{Sessions: h.Sessions()})
}

// upstream returns a connected session handle, attaching if needed.
func (h *Hub) upstream(ctx context.Context, sessionID string) (*jcode.Session, error) {
	a := h.attachmentFor(sessionID)
	select {
	case <-a.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if a.err != nil {
		h.dropAttachment(a)
		return nil, a.err
	}
	a.mu.Lock()
	s := a.sess
	if len(a.viewers) == 0 {
		a.armIdle()
	}
	a.mu.Unlock()
	if s == nil {
		return nil, errors.New("session detached")
	}
	return s, nil
}

func (a *attachment) run() {
	ctx := context.Background()
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	s, err := jcode.Attach(actx, a.h.cfg.MainSocket, a.id)
	cancel()
	if err != nil {
		a.err = fmt.Errorf("attach %s: %w", a.id, err)
		close(a.ready)
		return
	}
	// History is the first event after subscribe.
	select {
	case env, ok := <-s.Events():
		if ok && env.Type == "history" {
			var hist jcode.History
			_ = json.Unmarshal(env.Raw, &hist)
			a.history = hist.Messages
		} else if ok {
			a.tail = append(a.tail, env)
		}
	case <-time.After(30 * time.Second):
	}
	a.sess = s
	a.mu.Lock()
	a.armIdle()
	a.mu.Unlock()
	close(a.ready)
	a.h.log.Info("attached", "session", a.id, "history", len(a.history))

	for env := range s.Events() {
		a.dispatch(env)
	}
	a.h.log.Info("detached", "session", a.id, "error", s.Err())
	a.mu.Lock()
	a.sess = nil
	for ch := range a.viewers {
		close(ch)
	}
	a.viewers = map[chan jcode.Envelope]struct{}{}
	a.mu.Unlock()
	a.h.dropAttachment(a)
}

func (a *attachment) dispatch(env jcode.Envelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if env.Type == "history" {
		var hist jcode.History
		if json.Unmarshal(env.Raw, &hist) == nil {
			a.history = hist.Messages
			a.tail = a.tail[:0]
		}
	} else if keepInTail(env.Type) {
		if len(a.tail) >= maxTail {
			a.tail = append(a.tail[:0], a.tail[len(a.tail)/2:]...)
		}
		a.tail = append(a.tail, env)
	}
	for ch := range a.viewers {
		select {
		case ch <- env:
		default:
			// Viewer too slow. Close so the client reattaches with a fresh snapshot.
			close(ch)
			delete(a.viewers, ch)
		}
	}
}

// keepInTail filters noise that new viewers do not need replayed.
func keepInTail(t string) bool {
	switch t {
	case "mcp_status", "pong", "ack", "swarm_status", "available_models_updated", "model_catalog":
		return false
	}
	return true
}

func (a *attachment) removeViewer(ch chan jcode.Envelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.viewers[ch]; ok {
		delete(a.viewers, ch)
		close(ch)
	}
	if len(a.viewers) == 0 {
		a.armIdle()
	}
}

// armIdle closes the upstream after AttachLinger with no viewers.
// Caller holds a.mu.
func (a *attachment) armIdle() {
	if a.idle != nil {
		a.idle.Stop()
	}
	a.idle = time.AfterFunc(a.h.cfg.AttachLinger, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if len(a.viewers) == 0 && a.sess != nil {
			a.sess.Close()
		}
	})
}
