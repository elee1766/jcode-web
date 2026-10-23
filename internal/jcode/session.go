package jcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Session is an attached main-socket connection to one jcode session.
// Requests are correlated by id. Unsolicited events go to Events.
type Session struct {
	ID string

	c       *conn
	events  chan Envelope
	mu      sync.Mutex
	pending map[uint64]chan Envelope
	done    chan struct{}
	err     error
	subID   uint64

	subscribed atomic.Bool
}

// HistoryMessage mirrors jcode's history entry.
type HistoryMessage struct {
	Role     string    `json:"role"`
	Content  string    `json:"content"`
	ToolData *ToolCall `json:"tool_data,omitempty"`
}

// ToolCall mirrors jcode's tool_data.
type ToolCall struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
	Intent string          `json:"intent"`
}

// History is the history event sent on subscribe.
type History struct {
	SessionID string           `json:"session_id"`
	Messages  []HistoryMessage `json:"messages"`
}

// Attach subscribes to an existing live or persisted session. It does not
// take over other clients. The first event on Events is the history.
func Attach(ctx context.Context, path, sessionID string) (*Session, error) {
	c, err := dial(ctx, path)
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID:      sessionID,
		c:       c,
		events:  make(chan Envelope, 1024),
		pending: map[uint64]chan Envelope{},
		done:    make(chan struct{}),
	}
	id := c.id()
	s.subID = id
	ack := s.expect(id)
	go s.readLoop()
	err = c.send(map[string]any{
		"type":              "subscribe",
		"id":                id,
		"target_session_id": sessionID,
		// Keep turns started from this connection alive if we drop.
		"continue_on_disconnect": true,
	})
	if err != nil {
		s.Close()
		return nil, err
	}
	if _, err := s.wait(ctx, id, ack); err != nil {
		s.Close()
		return nil, err
	}
	s.subscribed.Store(true)
	return s, nil
}

// Events delivers server events. It closes when the connection ends.
func (s *Session) Events() <-chan Envelope { return s.events }

// Done closes when the connection ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err reports why the connection ended.
func (s *Session) Err() error {
	<-s.done
	return s.err
}

func (s *Session) Close() error { return s.c.c.Close() }

func (s *Session) expect(id uint64) chan Envelope {
	ch := make(chan Envelope, 8)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	return ch
}

func (s *Session) forget(id uint64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Session) readLoop() {
	defer close(s.done)
	defer close(s.events)
	sc := newScanner(s.c.c)
	for sc.Scan() {
		env, err := decodeEnvelope(sc.Bytes())
		if err != nil {
			continue
		}
		// Correlated replies. history carries the subscribe id but is also
		// content, so it goes to both.
		if env.ID != nil {
			// subscribe sends a second done after the bootstrap. Drop it once
			// the attach has completed so it does not look like a turn end.
			if *env.ID == s.subID && env.Type == "done" && s.subscribed.Load() {
				continue
			}
			s.mu.Lock()
			ch := s.pending[*env.ID]
			s.mu.Unlock()
			if ch != nil {
				select {
				case ch <- env:
				default:
				}
				if env.Type != "history" {
					continue
				}
			}
		}
		select {
		case s.events <- env:
		default:
			s.err = errors.New("session event buffer overflow: reattach to recover history")
			_ = s.c.c.Close()
			return
		}
	}
	s.err = sc.Err()
	if s.err == nil {
		s.err = errors.New("connection closed")
	}
}

// wait returns the first terminal reply for id: done, error, or a typed
// response. ack is skipped.
func (s *Session) wait(ctx context.Context, id uint64, ch chan Envelope) (Envelope, error) {
	defer s.forget(id)
	for {
		select {
		case env := <-ch:
			switch env.Type {
			case "ack", "history":
				continue
			case "error":
				var e struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(env.Raw, &e)
				return env, fmt.Errorf("jcode: %s", e.Message)
			default:
				return env, nil
			}
		case <-s.done:
			return Envelope{}, s.err
		case <-ctx.Done():
			return Envelope{}, ctx.Err()
		}
	}
}

// request sends req with a fresh id and waits for a terminal reply.
func (s *Session) request(ctx context.Context, req map[string]any) (Envelope, error) {
	id := s.c.id()
	req["id"] = id
	ch := s.expect(id)
	if err := s.c.send(req); err != nil {
		s.forget(id)
		return Envelope{}, err
	}
	return s.wait(ctx, id, ch)
}

// fire sends req without waiting past ack. Used for turns, whose done
// arrives when the turn ends.
func (s *Session) fire(ctx context.Context, req map[string]any) error {
	id := s.c.id()
	req["id"] = id
	ch := s.expect(id)
	if err := s.c.send(req); err != nil {
		s.forget(id)
		return err
	}
	defer s.forget(id)
	select {
	case env := <-ch:
		if env.Type == "error" {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(env.Raw, &e)
			return fmt.Errorf("jcode: %s", e.Message)
		}
		return nil
	case <-s.done:
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Message starts a turn.
func (s *Session) Message(ctx context.Context, content string) error {
	return s.fire(ctx, map[string]any{"type": "message", "content": content})
}

// SoftInterrupt queues content for the running turn.
func (s *Session) SoftInterrupt(ctx context.Context, content string, urgent bool) error {
	return s.fire(ctx, map[string]any{"type": "soft_interrupt", "content": content, "urgent": urgent})
}

// Cancel stops the running turn.
func (s *Session) Cancel(ctx context.Context) error {
	return s.fire(ctx, map[string]any{"type": "cancel"})
}

// RequestHistory asks for a fresh history event, delivered on Events.
func (s *Session) RequestHistory(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	id := s.c.id()
	ch := s.expect(id)
	defer s.forget(id)
	if err := s.c.send(map[string]any{"type": "get_history", "id": id}); err != nil {
		return err
	}
	for {
		select {
		case env := <-ch:
			switch env.Type {
			case "history", "done":
				return nil
			case "error":
				return fmt.Errorf("jcode: get_history failed")
			}
		case <-s.done:
			return s.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Split forks the session with its history. Works while busy.
func (s *Session) Split(ctx context.Context) (id, name string, err error) {
	env, err := s.request(ctx, map[string]any{"type": "split"})
	if err != nil {
		return "", "", err
	}
	if env.Type != "split_response" {
		return "", "", fmt.Errorf("split: unexpected %s", env.Type)
	}
	var r struct {
		NewSessionID   string `json:"new_session_id"`
		NewSessionName string `json:"new_session_name"`
	}
	if err := json.Unmarshal(env.Raw, &r); err != nil {
		return "", "", err
	}
	return r.NewSessionID, r.NewSessionName, nil
}
