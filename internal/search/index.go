package search

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	v1 "github.com/elee1766/jcode-web/gen/jcodeweb/v1"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Config struct {
	Enabled      bool          `koanf:"enabled"`
	SessionsDir  string        `koanf:"sessions_dir"`
	PollInterval time.Duration `koanf:"poll_interval"`
}

type Index struct {
	cfg            Config
	log            *slog.Logger
	writer, reader *sql.DB
	cancel         context.CancelFunc
	done           chan struct{}
	historyGate    chan struct{}
	failures       map[string]failedSource
	mu             sync.RWMutex
	status         v1.SearchIndexStatus
}

// Open starts one background indexer. Failures are exposed through Status and
// do not prevent the rest of jcode-web from serving requests.
func Open(ctx context.Context, path string, cfg Config, log *slog.Logger) *Index {
	ctx, cancel := context.WithCancel(ctx)
	x := &Index{cfg: cfg, log: log, cancel: cancel, done: make(chan struct{}), historyGate: make(chan struct{}, 1), failures: make(map[string]failedSource), status: v1.SearchIndexStatus{Enabled: cfg.Enabled}}
	if !cfg.Enabled {
		x.status.Error = "session search disabled"
		close(x.done)
		return x
	}
	if cfg.PollInterval < time.Second {
		x.cfg.PollInterval = 15 * time.Second
	}
	if err := x.openDB(ctx, path); err != nil {
		x.status.Error = err.Error()
		x.log.Warn("session search unavailable", "error", err)
		close(x.done)
		return x
	}
	x.status.Available = true
	go func() { defer close(x.done); x.run(ctx) }()
	return x
}
func (x *Index) openDB(ctx context.Context, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	u := url.URL{Scheme: "file", Path: path}
	params := "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-8192)&_pragma=foreign_keys(ON)"
	w, err := sql.Open("sqlite", u.String()+params)
	if err != nil {
		return err
	}
	w.SetMaxOpenConns(1)
	ok := false
	defer func() {
		if !ok {
			w.Close()
		}
	}()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, w, sub)
	if err != nil {
		return err
	}
	if _, err = p.Up(ctx); err != nil {
		return fmt.Errorf("search migration (FTS5 trigram required): %w", err)
	}
	r, err := sql.Open("sqlite", u.String()+params+"&_pragma=query_only(ON)")
	if err != nil {
		return err
	}
	r.SetMaxOpenConns(2)
	if err = r.PingContext(ctx); err != nil {
		r.Close()
		return err
	}
	x.writer, x.reader = w, r
	ok = true
	return nil
}
func (x *Index) Close() error {
	x.cancel()
	<-x.done
	var err error
	if x.reader != nil {
		err = x.reader.Close()
	}
	if x.writer != nil {
		err = errors.Join(err, x.writer.Close())
	}
	return err
}
func (x *Index) Status() *v1.SearchIndexStatus {
	x.mu.RLock()
	defer x.mu.RUnlock()
	s := &x.status
	return &v1.SearchIndexStatus{Enabled: s.Enabled, Available: s.Available, Indexing: s.Indexing, TotalSessions: s.TotalSessions, IndexedSessions: s.IndexedSessions, FailedSessions: s.FailedSessions, LastScanUnix: s.LastScanUnix, Error: s.Error}
}
func (x *Index) progress(ctx context.Context, lastErr string) {
	var indexed, failed uint64
	err := x.writer.QueryRowContext(ctx, `SELECT COALESCE(SUM(generation<>0),0),COALESCE(SUM(error<>''),0) FROM sessions`).Scan(&indexed, &failed)
	x.mu.Lock()
	defer x.mu.Unlock()
	if err != nil {
		x.status.Error = err.Error()
		return
	}
	x.status.IndexedSessions, x.status.FailedSessions = indexed, failed
	if lastErr != "" {
		x.status.Error = lastErr
	}
}

func (x *Index) run(ctx context.Context) {
	if err := x.cleanup(ctx); err != nil {
		x.mu.Lock()
		x.status.Error = err.Error()
		x.mu.Unlock()
	}
	for {
		x.scan(ctx)
		t := time.NewTimer(x.cfg.PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

type source struct {
	id                string
	snapshot, journal stamp
}

type failedSource struct {
	key, message string
	retryAt      time.Time
}

func (x *Index) scan(ctx context.Context) {
	started := time.Now()
	entries, err := os.ReadDir(x.cfg.SessionsDir)
	if err != nil {
		x.mu.Lock()
		x.status.Available = false
		x.status.Error = "read session directory: " + err.Error()
		x.status.Indexing = false
		x.mu.Unlock()
		return
	}
	var sources []source
	seen := map[string]bool{}
	var scanErr string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !safeID(id) {
			continue
		}
		seen[id] = true
		s := source{id: id}
		s.snapshot, err = fileStamp(filepath.Join(x.cfg.SessionsDir, e.Name()))
		if err == nil {
			s.journal, err = fileStamp(filepath.Join(x.cfg.SessionsDir, id+".journal.jsonl"))
		}
		if err != nil {
			scanErr = id + ": " + err.Error()
			x.recordError(ctx, id, scanErr)
			continue
		}
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].snapshot.Modified > sources[j].snapshot.Modified })
	x.mu.Lock()
	x.status.Available = true
	x.status.Indexing = true
	x.status.TotalSessions = uint64(len(seen))
	x.status.Error = scanErr
	x.mu.Unlock()
	x.progress(ctx, scanErr)
	changed := 0
	for _, s := range sources {
		if ctx.Err() != nil {
			return
		}
		key := s.snapshot.key() + "/" + s.journal.key()
		if failure, ok := x.failures[s.id]; ok && failure.key == key && time.Now().Before(failure.retryAt) {
			scanErr = failure.message
			continue
		}
		updated, e := x.indexSession(ctx, s)
		if updated {
			changed++
		}
		if e != nil {
			scanErr = s.id + ": " + e.Error()
			x.failures[s.id] = failedSource{key: key, message: scanErr, retryAt: time.Now().Add(time.Minute)}
			x.recordError(ctx, s.id, scanErr)
			x.log.Warn("session indexing failed", "session", s.id, "error", e)
		} else {
			delete(x.failures, s.id)
		}
		x.progress(ctx, scanErr)
	}
	if ctx.Err() != nil {
		return
	}
	for id := range x.failures {
		if !seen[id] {
			delete(x.failures, id)
		}
	}
	// Remove deleted histories only after a successful directory listing.
	rows, e := x.writer.QueryContext(ctx, `SELECT id FROM sessions`)
	var deleted []string
	if e == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && !seen[id] {
				deleted = append(deleted, id)
			}
		}
		e = rows.Err()
		rows.Close()
	}
	if e != nil {
		scanErr = e.Error()
	} else {
		for _, id := range deleted {
			if _, e = x.writer.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id); e != nil {
				scanErr = e.Error()
				break
			}
		}
	}
	if e = x.cleanup(ctx); e != nil {
		scanErr = e.Error()
	}
	x.progress(ctx, scanErr)
	x.mu.Lock()
	x.status.Indexing = false
	x.status.LastScanUnix = time.Now().Unix()
	x.status.Error = scanErr
	x.mu.Unlock()
	x.log.Info("session search scan", "sessions", len(seen), "changed", changed, "elapsed", time.Since(started), "error", scanErr)
	// Readers can continue using their WAL snapshots during checkpoints.
	_, _ = x.writer.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
}
func (x *Index) recordError(ctx context.Context, id, msg string) {
	_, err := x.writer.ExecContext(ctx, `INSERT INTO sessions(id,error) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET error=excluded.error`, id, msg)
	if err != nil {
		x.log.Warn("record search error", "error", err)
	}
}

// Retire unpublished work in small transactions, including a crash midway
// through a rebuild or an append whose source changed before publication.
func (x *Index) cleanup(ctx context.Context) error {
	var cursor int64
	for {
		rows, err := x.writer.QueryContext(ctx, `SELECT c.id FROM chunks c LEFT JOIN sessions s ON s.id=c.session_id
   WHERE c.id>? AND (s.id IS NULL OR c.generation<>s.generation OR c.ordinal>=s.message_count) ORDER BY c.id LIMIT 128`, cursor)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		cursor = ids[len(ids)-1]
		_, err = x.writer.ExecContext(ctx, `DELETE FROM chunks WHERE id>=? AND id<=? AND NOT EXISTS (
   SELECT 1 FROM sessions s WHERE s.id=chunks.session_id AND s.generation=chunks.generation AND chunks.ordinal<s.message_count)`, ids[0], cursor)
		if err != nil {
			return err
		}
		if err = pause(ctx); err != nil {
			return err
		}
	}
	_, err := x.writer.ExecContext(ctx, `DELETE FROM messages WHERE NOT EXISTS (
  SELECT 1 FROM sessions s WHERE s.id=messages.session_id AND s.generation=messages.generation AND messages.ordinal<s.message_count)`)
	return err
}
func pause(ctx context.Context) error {
	t := time.NewTimer(time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type savedState struct {
	meta                    metadata
	generation              int64
	snapshotKey, journalKey string
	journalInode            uint64
	offset, count           int64
	error                   string
}

func (x *Index) state(ctx context.Context, id string) (savedState, error) {
	var s savedState
	var raw string
	err := x.writer.QueryRowContext(ctx, `SELECT metadata,generation,snapshot_key,journal_key,journal_inode,journal_offset,message_count,error FROM sessions WHERE id=?`, id).Scan(&raw, &s.generation, &s.snapshotKey, &s.journalKey, &s.journalInode, &s.offset, &s.count, &s.error)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(raw), &s.meta)
	return s, err
}

func (x *Index) indexSession(ctx context.Context, s source) (bool, error) {
	old, err := x.state(ctx, s.id)
	if err != nil {
		return false, err
	}
	if old.generation != 0 && old.snapshotKey == s.snapshot.key() && old.journalKey == s.journal.key() {
		if old.error != "" {
			if _, err := x.writer.ExecContext(ctx, `UPDATE sessions SET error='' WHERE id=?`, s.id); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	rebuild := old.generation == 0 || old.snapshotKey != s.snapshot.key() || old.journalInode != s.journal.Inode || s.journal.Size < old.offset ||
		(s.journal.Size == old.offset && old.journalKey != s.journal.key())
	gen, count, offset, meta := old.generation, old.count, old.offset, old.meta
	if rebuild {
		gen = time.Now().UnixNano()
		count = 0
		offset = 0
	}
	b := &batchWriter{x: x, ctx: ctx, id: s.id, generation: gen, count: count}
	defer b.rollback()
	apply := func(m message) error { return b.message(m) }
	if rebuild {
		meta, err = readSnapshot(ctx, filepath.Join(x.cfg.SessionsDir, s.id+".json"), s.snapshot, true, apply)
		if err != nil {
			return false, err
		}
		if meta.ID != s.id {
			return false, errors.New("snapshot id does not match filename")
		}
	}
	snapshotUpdated := meta.UpdatedAt
	stalePrefix := rebuild
	offset, err = readJournal(ctx, filepath.Join(x.cfg.SessionsDir, s.id+".journal.jsonl"), s.journal, offset, true, func(e journalEntry) error {
		before := b.count
		for _, m := range e.Messages {
			if err := apply(m); err != nil {
				return err
			}
		}
		stale := stalePrefix && ((len(e.Messages) == 0 && !e.Meta.UpdatedAt.After(snapshotUpdated)) || (len(e.Messages) > 0 && before == b.count))
		if !stale {
			stalePrefix = false
			meta = e.Meta
			meta.ID = s.id
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if err = b.commit(); err != nil {
		return false, err
	}
	// Snapshots are atomically replaced upstream. A concurrent checkpoint must
	// not publish a mixed old-snapshot/new-journal view.
	after, err := fileStamp(filepath.Join(x.cfg.SessionsDir, s.id+".json"))
	if err != nil {
		return false, err
	}
	journalAfter, err := fileStamp(filepath.Join(x.cfg.SessionsDir, s.id+".journal.jsonl"))
	if err != nil {
		return false, err
	}
	if after != s.snapshot || journalAfter.Inode != s.journal.Inode || journalAfter.Size < s.journal.Size {
		return false, errors.New("history checkpoint changed during indexing, retrying next scan")
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return false, err
	}
	_, err = x.writer.ExecContext(ctx, `INSERT INTO sessions(id,metadata,generation,snapshot_key,journal_key,journal_inode,journal_offset,message_count,error)
 VALUES(?,?,?,?,?,?,?,?,'') ON CONFLICT(id) DO UPDATE SET metadata=excluded.metadata,generation=excluded.generation,snapshot_key=excluded.snapshot_key,journal_key=excluded.journal_key,journal_inode=excluded.journal_inode,journal_offset=excluded.journal_offset,message_count=excluded.message_count,error=''`, s.id, string(raw), gen, s.snapshot.key(), s.journal.key(), s.journal.Inode, offset, b.count)
	return err == nil, err
}

type batchWriter struct {
	x                 *Index
	ctx               context.Context
	id                string
	generation, count int64
	tx                *sql.Tx
	chunks            int
}

func (b *batchWriter) begin() error {
	if b.tx != nil {
		return nil
	}
	tx, err := b.x.writer.BeginTx(b.ctx, nil)
	b.tx = tx
	return err
}
func (b *batchWriter) rollback() {
	if b.tx != nil {
		b.tx.Rollback()
		b.tx = nil
	}
}
func (b *batchWriter) commit() error {
	if b.tx == nil {
		return nil
	}
	err := b.tx.Commit()
	b.tx = nil
	b.chunks = 0
	if err != nil {
		return err
	}
	return pause(b.ctx)
}
func (b *batchWriter) message(m message) error {
	if err := b.begin(); err != nil {
		return err
	}
	if m.ID == "" {
		return errors.New("saved message has no id")
	}
	res, err := b.tx.ExecContext(b.ctx, `INSERT OR IGNORE INTO messages(session_id,generation,message_id,ordinal) VALUES(?,?,?,?)`, b.id, b.generation, m.ID, b.count)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	ordinal := b.count
	b.count++
	b.chunks++
	if b.chunks >= 128 {
		if err = b.commit(); err != nil {
			return err
		}
	}
	role := m.Role
	if m.DisplayRole != "" {
		role = m.DisplayRole
	}
	text := m.text()
	for len(text) > 0 {
		end, runes := 0, 0
		for pos := range text {
			if runes == 4096 {
				end = pos
				break
			}
			runes++
		}
		if end == 0 {
			end = len(text)
		}
		body := text[:end]
		if err = b.begin(); err != nil {
			return err
		}
		_, err = b.tx.ExecContext(b.ctx, `INSERT INTO chunks(session_id,generation,ordinal,role,body,folded) VALUES(?,?,?,?,?,?)`, b.id, b.generation, ordinal, role, body, strings.ToLower(body))
		if err != nil {
			return err
		}
		b.chunks++
		if b.chunks >= 128 {
			if err = b.commit(); err != nil {
				return err
			}
		}
		if end == len(text) {
			break
		}
		// 255 characters of overlap preserve every allowed query across chunks.
		next := end
		for range 255 {
			_, size := utf8.DecodeLastRuneInString(text[:next])
			next -= size
		}
		text = text[next:]
	}
	return nil
}
