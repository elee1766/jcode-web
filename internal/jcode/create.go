package jcode

import (
	"context"
	"encoding/json"
	"fmt"
)

// Create starts a normal session through the main socket, not an isolated
// debug/test session. No prompt is sent until the user sends one.
func Create(ctx context.Context, path, dir string) (string, error) {
	c, err := dial(ctx, path)
	if err != nil {
		return "", err
	}
	defer c.c.Close()
	stop := context.AfterFunc(ctx, func() { c.c.Close() })
	defer stop()
	if err := c.send(map[string]any{"type": "subscribe", "id": 1, "working_dir": dir, "continue_on_disconnect": true}); err != nil {
		return "", err
	}
	scanner := newScanner(c.c)
	var sessionID string
	for scanner.Scan() {
		var event struct {
			Type      string `json:"type"`
			ID        uint64 `json:"id"`
			SessionID string `json:"session_id"`
			Message   string `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "error" {
			return "", fmt.Errorf("create session: %s", event.Message)
		}
		if (event.Type == "session" || event.Type == "history") && event.SessionID != "" {
			sessionID = event.SessionID
		}
		if event.Type == "done" && event.ID == 1 && sessionID != "" {
			return sessionID, nil
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if scanner.Err() != nil {
		return "", scanner.Err()
	}
	return "", fmt.Errorf("create session: connection closed before subscription completed")
}
