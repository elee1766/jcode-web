package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/elee1766/jcode-web/gen/jcodeweb/v1"
)

var ErrQuery = errors.New("query must contain 3 to 256 Unicode characters and no NUL")

// Search uses indexed trigram intersections, then verifies the exact folded
// substring only in candidate chunks. No history files are read on this path.
func (x *Index) Search(ctx context.Context, query string, limit uint32, sessionID string) (*v1.SearchSessionsResponse, error) {
	n := utf8.RuneCountInString(query)
	if !utf8.ValidString(query) || n < 3 || n > 256 || strings.ContainsRune(query, 0) {
		return nil, ErrQuery
	}
	out := &v1.SearchSessionsResponse{Status: x.Status()}
	if x.reader == nil {
		return out, nil
	}
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	folded := strings.ToLower(query)
	match := trigrams(folded)
	tx, err := x.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := `SELECT DISTINCT s.id,s.metadata
 FROM chunks_fts JOIN chunks c ON c.id=chunks_fts.rowid
 JOIN sessions s ON s.id=c.session_id AND s.generation=c.generation AND c.ordinal<s.message_count
 WHERE chunks_fts MATCH ? AND instr(c.folded,?)>0`
	args := []any{match, folded}
	if sessionID != "" {
		q += ` AND c.session_id=?`
		args = append(args, sessionID)
	}
	q += ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type hit struct{ id, raw string }
	var hits []hit
	for rows.Next() {
		var h hit
		if err = rows.Scan(&h.id, &h.raw); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(hits) > int(limit) {
		out.Truncated = true
		hits = hits[:limit]
	}
	for _, h := range hits {
		var meta metadata
		if err = json.Unmarshal([]byte(h.raw), &meta); err != nil {
			return nil, err
		}
		name := meta.Name
		if name == "" {
			name = meta.CustomTitle
		}
		if name == "" {
			name = meta.Title
		}
		if name == "" {
			name = h.id
		}
		result := &v1.SessionSearchResult{Session: &v1.Session{Id: h.id, Name: name, Status: "archived", WorkingDir: meta.WorkingDir, Model: meta.Model, Provider: meta.Provider, ParentSessionId: meta.ParentID, LastEventUnix: meta.UpdatedAt.Unix()}}
		rs, e := tx.QueryContext(ctx, `SELECT c.body,c.role,c.ordinal FROM chunks_fts JOIN chunks c ON c.id=chunks_fts.rowid
   JOIN sessions s ON s.id=c.session_id AND s.generation=c.generation AND c.ordinal<s.message_count
   WHERE chunks_fts MATCH ? AND c.session_id=? AND instr(c.folded,?)>0 LIMIT 12`, match, h.id, folded)
		if e != nil {
			return nil, e
		}
		seen := map[int64]bool{}
		for rs.Next() {
			var body, role string
			var ordinal int64
			if e = rs.Scan(&body, &role, &ordinal); e != nil {
				break
			}
			if seen[ordinal] {
				continue
			}
			seen[ordinal] = true
			result.Excerpts = append(result.Excerpts, &v1.SessionSearchExcerpt{Text: excerpt(body, folded), Role: role, MessageIndex: uint32(ordinal)})
			if len(result.Excerpts) == 3 {
				break
			}
		}
		rowErr := rs.Err()
		rs.Close()
		if e != nil {
			return nil, e
		}
		if rowErr != nil {
			return nil, rowErr
		}
		out.Results = append(out.Results, result)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func trigrams(text string) string {
	runes := []rune(text)
	seen := map[string]bool{}
	var terms []string
	for i := 0; i+3 <= len(runes); i++ {
		tri := string(runes[i : i+3])
		if seen[tri] {
			continue
		}
		seen[tri] = true
		terms = append(terms, `"`+strings.ReplaceAll(tri, `"`, `""`)+`"`)
	}
	// A subset is sufficient because candidate chunks are checked with instr.
	// Spread terms across long queries without building hundreds of FTS nodes.
	if len(terms) > 24 {
		selected := make([]string, 24)
		for i := range selected {
			selected[i] = terms[i*(len(terms)-1)/(len(selected)-1)]
		}
		terms = selected
	}
	return strings.Join(terms, " AND ")
}
func excerpt(body, query string) string {
	lower := strings.ToLower(body)
	pos := strings.Index(lower, query)
	if pos < 0 {
		return ""
	}
	start := utf8.RuneCountInString(lower[:pos])
	runes := []rune(body)
	from := max(0, start-80)
	to := min(len(runes), start+utf8.RuneCountInString(query)+160)
	text := string(runes[from:to])
	if from > 0 {
		text = "…" + text
	}
	if to < len(runes) {
		text += "…"
	}
	return text
}

// SavedHistory returns a bounded trailing view for an archived session. It
// never subscribes, resumes, writes, or changes agent state.
func (x *Index) SavedHistory(ctx context.Context, id string, limit uint32) ([]*v1.HistoryMessage, uint32, error) {
	if !safeID(id) || !x.cfg.Enabled {
		return nil, 0, fmt.Errorf("saved history unavailable")
	}
	if limit == 0 || limit > 1000 {
		limit = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case x.historyGate <- struct{}{}:
		defer func() { <-x.historyGate }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	path := x.cfg.SessionsDir + "/" + id + ".json"
	snapshot, err := fileStamp(path)
	if err != nil {
		return nil, 0, err
	}
	if snapshot.Inode == 0 {
		return nil, 0, fmt.Errorf("saved session not found")
	}
	journalPath := x.cfg.SessionsDir + "/" + id + ".journal.jsonl"
	journal, err := fileStamp(journalPath)
	if err != nil {
		return nil, 0, err
	}
	seen := map[string]bool{}
	var total uint32
	var out []*v1.HistoryMessage
	size := 0
	apply := func(m message) error {
		if m.ID == "" {
			return errors.New("saved message has no id")
		}
		if seen[m.ID] {
			return nil
		}
		seen[m.ID] = true
		if len(seen) > 1000000 {
			return errors.New("saved history exceeds message limit")
		}
		for _, h := range m.history() {
			total++
			pb := &v1.HistoryMessage{Role: h.Role, Content: h.Content}
			bytes := len(h.Content)
			if h.ToolData != nil {
				pb.Tool = &v1.ToolData{Id: h.ToolData.ID, Name: h.ToolData.Name, InputJson: string(h.ToolData.Input), Intent: h.ToolData.Intent}
				bytes += len(pb.Tool.InputJson)
			}
			if bytes > 8<<20 {
				pb = &v1.HistoryMessage{Role: h.Role, Content: "[saved row exceeds 8 MiB display limit]"}
				bytes = len(pb.Content)
			}
			out = append(out, pb)
			size += bytes
			for len(out) > int(limit) || size > 8<<20 {
				size -= len(out[0].Content)
				if out[0].Tool != nil {
					size -= len(out[0].Tool.InputJson)
				}
				out[0] = nil
				out = out[1:]
			}
		}
		return nil
	}
	meta, err := readSnapshot(ctx, path, snapshot, true, apply)
	if err != nil {
		return nil, 0, err
	}
	if meta.ID != id {
		return nil, 0, errors.New("saved id does not match filename")
	}
	if _, err = readJournal(ctx, journalPath, journal, 0, true, func(e journalEntry) error {
		for _, m := range e.Messages {
			if err := apply(m); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, 0, err
	}
	after, err := fileStamp(path)
	if err != nil {
		return nil, 0, err
	}
	afterJournal, err := fileStamp(journalPath)
	if err != nil {
		return nil, 0, err
	}
	if after != snapshot || afterJournal.Inode != journal.Inode || afterJournal.Size < journal.Size {
		return nil, 0, errors.New("saved history changed while opening, retry")
	}
	return out, total, nil
}
