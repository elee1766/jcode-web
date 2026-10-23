// Package search indexes saved jcode histories without connecting to agents.
package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/elee1766/jcode-web/internal/jcode"
)

const maxRecordBytes = 64 << 20
const maxSourceBytes = 1 << 30

type metadata struct {
	ID          string    `json:"id"`
	ParentID    string    `json:"parent_id"`
	Title       string    `json:"title"`
	CustomTitle string    `json:"custom_title"`
	Name        string    `json:"short_name"`
	WorkingDir  string    `json:"working_dir"`
	Model       string    `json:"model"`
	Provider    string    `json:"provider_key"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Summary   []string        `json:"summary"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   string          `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
	ToolName  string          `json:"tool_name"`
}
type message struct {
	ID          string  `json:"id"`
	Role        string  `json:"role"`
	DisplayRole string  `json:"display_role"`
	Content     []block `json:"content"`
}

func (m message) text() string {
	var parts []string
	for _, b := range m.Content {
		switch b.Type {
		case "text", "reasoning", "reasoning_trace":
			parts = append(parts, b.Text)
		case "anthropic_thinking":
			parts = append(parts, b.Thinking)
		case "openai_reasoning":
			parts = append(parts, b.Summary...)
		case "tool_use":
			parts = append(parts, b.Name+"\n"+string(b.Input))
		case "tool_result":
			parts = append(parts, b.Content)
		case "tool_reference":
			parts = append(parts, b.ToolName)
		}
	}
	return strings.Join(parts, "\n")
}

func (m message) history() []jcode.HistoryMessage {
	var out []jcode.HistoryMessage
	for _, b := range m.Content {
		role := m.Role
		if m.DisplayRole != "" {
			role = m.DisplayRole
		}
		h := jcode.HistoryMessage{Role: role}
		switch b.Type {
		case "text":
			h.Content = b.Text
		case "reasoning", "reasoning_trace":
			h.Role, h.Content = "reasoning", b.Text
		case "anthropic_thinking":
			h.Role, h.Content = "reasoning", b.Thinking
		case "openai_reasoning":
			h.Role, h.Content = "reasoning", strings.Join(b.Summary, "\n")
		case "tool_use":
			h.Role, h.ToolData = "tool", &jcode.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input}
		case "tool_result":
			h.Role, h.Content = "tool", b.Content
		case "image":
			h.Content = "[saved image]"
		case "tool_reference":
			h.Content = b.ToolName
		default:
			continue
		}
		if h.Content != "" || h.ToolData != nil {
			out = append(out, h)
		}
	}
	return out
}

type stamp struct {
	Size, Modified int64
	Inode          uint64
}

func fileStamp(path string) (stamp, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return stamp{}, nil
	}
	if err != nil {
		return stamp{}, err
	}
	if !info.Mode().IsRegular() {
		return stamp{}, fmt.Errorf("not a regular history file: %s", filepath.Base(path))
	}
	if info.Size() > maxSourceBytes {
		return stamp{}, fmt.Errorf("history file exceeds %d bytes: %s", maxSourceBytes, filepath.Base(path))
	}
	s := stamp{Size: info.Size(), Modified: info.ModTime().UnixNano()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		s.Inode = st.Ino
	}
	return s, nil
}
func (s stamp) key() string { return fmt.Sprintf("%d:%d:%d", s.Size, s.Modified, s.Inode) }

// budgetReader bounds both individual JSON values and sustained source I/O.
type budgetReader struct {
	ctx           context.Context
	r             io.Reader
	read, ceiling int64
	started       time.Time
	pace          bool
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.read >= r.ceiling {
		return 0, fmt.Errorf("JSON value exceeds %d-byte record limit", maxRecordBytes)
	}
	if int64(len(p)) > r.ceiling-r.read {
		p = p[:r.ceiling-r.read]
	}
	if len(p) > 256<<10 {
		p = p[:256<<10]
	}
	if r.pace {
		delay := time.Duration(r.read)*time.Second/(32<<20) - time.Since(r.started)
		if delay > 0 {
			t := time.NewTimer(delay)
			select {
			case <-t.C:
			case <-r.ctx.Done():
				t.Stop()
				return 0, r.ctx.Err()
			}
		}
	}
	n, err := r.r.Read(p)
	r.read += int64(n)
	return n, err
}

func readSnapshot(ctx context.Context, path string, expected stamp, pace bool, apply func(message) error) (metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return metadata{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return metadata{}, err
	}
	if info.Size() != expected.Size || info.ModTime().UnixNano() != expected.Modified {
		return metadata{}, errors.New("snapshot changed before reading")
	}
	r := &budgetReader{ctx: ctx, r: io.LimitReader(f, expected.Size), ceiling: maxRecordBytes, started: time.Now(), pace: pace}
	d := json.NewDecoder(r)
	var meta metadata
	t, err := d.Token()
	if err != nil {
		return meta, err
	}
	if t != json.Delim('{') {
		return meta, errors.New("snapshot must be an object")
	}
	for d.More() {
		r.ceiling = d.InputOffset() + maxRecordBytes
		key, err := d.Token()
		if err != nil {
			return meta, err
		}
		if key == "messages" {
			t, err = d.Token()
			if err != nil {
				return meta, err
			}
			if t != json.Delim('[') {
				return meta, errors.New("messages must be an array")
			}
			for d.More() {
				r.ceiling = d.InputOffset() + maxRecordBytes
				var m message
				if err = d.Decode(&m); err != nil {
					return meta, err
				}
				if err = apply(m); err != nil {
					return meta, err
				}
			}
			if _, err = d.Token(); err != nil {
				return meta, err
			}
			continue
		}
		// Decode only one top-level field at a time, never the whole transcript.
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return meta, err
		}
		var dst any
		switch key {
		case "id":
			dst = &meta.ID
		case "parent_id":
			dst = &meta.ParentID
		case "title":
			dst = &meta.Title
		case "custom_title":
			dst = &meta.CustomTitle
		case "short_name":
			dst = &meta.Name
		case "working_dir":
			dst = &meta.WorkingDir
		case "model":
			dst = &meta.Model
		case "provider_key":
			dst = &meta.Provider
		case "updated_at":
			dst = &meta.UpdatedAt
		}
		if dst != nil {
			if err = json.Unmarshal(raw, dst); err != nil {
				return meta, err
			}
		}
	}
	if _, err = d.Token(); err != nil {
		return meta, err
	}
	r.ceiling = d.InputOffset() + maxRecordBytes
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return meta, errors.New("trailing data in snapshot")
	}
	return meta, nil
}

type journalEntry struct {
	Meta     metadata  `json:"meta"`
	Messages []message `json:"append_messages"`
}

// Only consume newline-terminated entries from a fixed file extent. A torn tail
// is retried next scan. Corrupt complete entries leave the last good index intact.
func readJournal(ctx context.Context, path string, expected stamp, offset int64, pace bool, apply func(journalEntry) error) (int64, error) {
	if expected.Inode == 0 && expected.Size == 0 {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	r := &budgetReader{ctx: ctx, r: io.LimitReader(f, expected.Size-offset), ceiling: maxSourceBytes, started: time.Now(), pace: pace}
	sc := bufio.NewReaderSize(r, 64<<10)
	for {
		var line []byte
		for {
			part, e := sc.ReadSlice('\n')
			if len(line)+len(part) > maxRecordBytes {
				return offset, fmt.Errorf("journal record exceeds %d bytes", maxRecordBytes)
			}
			line = append(line, part...)
			if e == bufio.ErrBufferFull {
				continue
			}
			err = e
			break
		}
		if err == io.EOF {
			return offset, nil
		}
		if err != nil {
			return offset, err
		}
		var entry journalEntry
		if len(strings.TrimSpace(string(line))) > 0 {
			if err = json.Unmarshal(line, &entry); err != nil {
				return offset, fmt.Errorf("journal at byte %d: %w", offset, err)
			}
			if err = apply(entry); err != nil {
				return offset, err
			}
		}
		offset += int64(len(line))
	}
}

func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, "/\\\x00")
}
