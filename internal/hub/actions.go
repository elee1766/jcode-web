package hub

import (
	"context"
	"fmt"
	"time"
)

type SendMode int

const (
	SendAuto SendMode = iota
	SendMessage
	SendInterrupt
	SendInterruptUrgent
)

// Send delivers content to a session. Auto starts a turn when idle and
// queues a soft interrupt when busy.
func (h *Hub) Send(ctx context.Context, sessionID, content string, mode SendMode) (SendMode, error) {
	if mode == SendAuto {
		mode = SendMessage
		if s, ok := h.session(sessionID); ok && s.IsProcessing {
			mode = SendInterrupt
		}
	}
	up, err := h.upstream(ctx, sessionID)
	if err != nil {
		return mode, err
	}
	switch mode {
	case SendMessage:
		err = up.Message(ctx, content)
	case SendInterrupt:
		err = up.SoftInterrupt(ctx, content, false)
	case SendInterruptUrgent:
		err = up.SoftInterrupt(ctx, content, true)
	default:
		err = fmt.Errorf("unknown send mode %d", mode)
	}
	return mode, err
}

func (h *Hub) Cancel(ctx context.Context, sessionID string) error {
	up, err := h.upstream(ctx, sessionID)
	if err != nil {
		return err
	}
	return up.Cancel(ctx)
}

// Btw forks sessionID with its history and asks question in the fork, like
// jcode's /btw. The parent is not interrupted.
func (h *Hub) Btw(ctx context.Context, sessionID, question string) (id, name string, err error) {
	parent, err := h.upstream(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	id, name, err = parent.Split(ctx)
	if err != nil {
		return "", "", fmt.Errorf("fork: %w", err)
	}
	child, err := h.upstream(ctx, id)
	if err != nil {
		return id, name, fmt.Errorf("attach fork: %w", err)
	}
	if err := child.Message(ctx, question); err != nil {
		return id, name, fmt.Errorf("ask fork: %w", err)
	}
	// Pick up the new session in the list without waiting for the poll.
	go func() {
		time.Sleep(500 * time.Millisecond)
		h.refresh(context.Background())
	}()
	return id, name, nil
}
