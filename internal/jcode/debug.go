// Package jcode talks to a jcode server over its main and debug Unix sockets.
// Both sockets carry newline-delimited JSON tagged by "type".
package jcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// maxLine bounds one NDJSON line. History events for long sessions are large.
const maxLine = 64 << 20

// DefaultSockets returns the main and debug socket paths for this user.
func DefaultSockets() (main, debug string) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(dir, "jcode.sock"), filepath.Join(dir, "jcode-debug.sock")
}

// Envelope holds the type tag and the raw line.
type Envelope struct {
	Type string `json:"type"`
	ID   *uint64 `json:"id,omitempty"`
	Raw  json.RawMessage `json:"-"`
}

func decodeEnvelope(line []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return env, err
	}
	env.Raw = append(json.RawMessage(nil), line...)
	return env, nil
}

// conn is one NDJSON connection with request-id correlation.
type conn struct {
	c      net.Conn
	wmu    sync.Mutex
	nextID atomic.Uint64
}

func dial(ctx context.Context, path string) (*conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return &conn{c: c}, nil
}

func (c *conn) id() uint64 { return c.nextID.Add(1) }

func (c *conn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.c.Write(b)
	return err
}

func newScanner(c net.Conn) *bufio.Scanner {
	s := bufio.NewScanner(c)
	s.Buffer(make([]byte, 0, 64<<10), maxLine)
	return s
}

// ErrDebugDisabled is returned when the server refuses debug commands.
var ErrDebugDisabled = errors.New("jcode debug control is disabled: create ~/.jcode/debug_control or set display.debug_socket")

// Debug runs commands on the debug socket. Each call uses its own connection
// so slow commands do not block others.
type Debug struct {
	Path string
}

type debugResponse struct {
	Type    string `json:"type"`
	ID      uint64 `json:"id"`
	OK      bool   `json:"ok"`
	Output  string `json:"output"`
	Message string `json:"message"`
}

// Run executes one debug command. sessionID may be empty.
func (d *Debug) Run(ctx context.Context, command, sessionID string) (string, error) {
	c, err := dial(ctx, d.Path)
	if err != nil {
		return "", err
	}
	defer c.c.Close()
	stop := context.AfterFunc(ctx, func() { c.c.Close() })
	defer stop()

	req := map[string]any{"type": "debug_command", "id": 1, "command": command}
	if sessionID != "" {
		req["session_id"] = sessionID
	}
	if err := c.send(req); err != nil {
		return "", err
	}
	s := newScanner(c.c)
	for s.Scan() {
		var r debugResponse
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			continue
		}
		switch r.Type {
		case "debug_response":
			if !r.OK {
				return "", fmt.Errorf("debug %q: %s", command, r.Output)
			}
			return r.Output, nil
		case "error":
			if isDebugDisabled(r.Message) {
				return "", ErrDebugDisabled
			}
			return "", fmt.Errorf("debug %q: %s", command, r.Message)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("debug %q: connection closed", command)
}

func isDebugDisabled(msg string) bool {
	return len(msg) >= 22 && msg[:22] == "Debug control is disab"
}

// SessionInfo is one entry from the debug "sessions" command.
type SessionInfo struct {
	SessionID    string `json:"session_id"`
	FriendlyName string `json:"friendly_name"`
	Status       string `json:"status"`
	IsProcessing bool   `json:"is_processing"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	WorkingDir   string `json:"working_dir"`
	SwarmID      string `json:"swarm_id"`
	ServerName   string `json:"server_name"`
	Detail       *string `json:"detail"`
	TokenUsage   struct {
		Input      uint64  `json:"input"`
		Output     uint64  `json:"output"`
		CacheRead  *uint64 `json:"cache_read"`
		CacheWrite *uint64 `json:"cache_write"`
	} `json:"token_usage"`
}

// Sessions lists live sessions.
func (d *Debug) Sessions(ctx context.Context) ([]SessionInfo, error) {
	out, err := d.Run(ctx, "sessions", "")
	if err != nil {
		return nil, err
	}
	var s []SessionInfo
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}
	return s, nil
}

// BusEvent is one event from events:subscribe.
type BusEvent struct {
	Type          string          `json:"type"`
	ID            uint64          `json:"id"`
	SessionID     string          `json:"session_id"`
	SessionName   string          `json:"session_name"`
	SwarmID       string          `json:"swarm_id"`
	Event         json.RawMessage `json:"event"`
	TimestampUnix int64           `json:"timestamp_unix"`
	Missed        uint64          `json:"missed"`
}

// EventType returns the inner event type tag.
func (e BusEvent) EventType() string {
	var t struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(e.Event, &t)
	return t.Type
}

// Subscribe streams bus events until ctx ends or the connection drops.
// Lag notices arrive with Type "lag".
func (d *Debug) Subscribe(ctx context.Context, fn func(BusEvent)) error {
	c, err := dial(ctx, d.Path)
	if err != nil {
		return err
	}
	defer c.c.Close()
	stop := context.AfterFunc(ctx, func() { c.c.Close() })
	defer stop()

	if err := c.send(map[string]any{"type": "debug_command", "id": 1, "command": "events:subscribe"}); err != nil {
		return err
	}
	s := newScanner(c.c)
	for s.Scan() {
		var ev BusEvent
		if err := json.Unmarshal(s.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "event", "lag":
			fn(ev)
		case "error":
			var r debugResponse
			_ = json.Unmarshal(s.Bytes(), &r)
			if isDebugDisabled(r.Message) {
				return ErrDebugDisabled
			}
			return fmt.Errorf("events:subscribe: %s", r.Message)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Err(); err != nil {
		return err
	}
	return errors.New("events:subscribe: connection closed")
}
