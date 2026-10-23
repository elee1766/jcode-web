package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/alecthomas/kong"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/elee1766/jcode-web/gen/jcodeweb/v1/jcodewebv1connect"
	"github.com/elee1766/jcode-web/internal/api"
	"github.com/elee1766/jcode-web/internal/config"
	"github.com/elee1766/jcode-web/internal/hub"
	"github.com/elee1766/jcode-web/internal/push"
	"github.com/elee1766/jcode-web/internal/search"
	"github.com/elee1766/jcode-web/internal/store"
	"github.com/elee1766/jcode-web/internal/summarize"
	"github.com/elee1766/jcode-web/web"
)

var version = "dev"

type CLI struct {
	Config string `short:"c" help:"Config file path." type:"path"`

	Serve   ServeCmd   `cmd:"" default:"withargs" help:"Run the web server."`
	Check   CheckCmd   `cmd:"" help:"Check that jcode sockets are reachable."`
	Version VersionCmd `cmd:"" help:"Print version."`
}

type ServeCmd struct {
	Listen  string `short:"l" help:"Listen address (overrides config)."`
	DataDir string `help:"Data directory (overrides config)." type:"path"`
}

type CheckCmd struct{}

type VersionCmd struct{}

func (VersionCmd) Run() error {
	fmt.Println(version)
	return nil
}

func loadConfig(cli *CLI, overrides map[string]any) (config.Config, error) {
	path, explicit := cli.Config, cli.Config != ""
	if !explicit {
		path = config.DefaultPath()
	}
	return config.Load(path, explicit, overrides)
}

func newLogger(c config.Config) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(c.LogLevel))
	opts := &slog.HandlerOptions{Level: lvl}
	if c.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func (cmd *CheckCmd) Run(cli *CLI) error {
	c, err := loadConfig(cli, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := hub.New(hub.Config{MainSocket: c.MainSocket, DebugSocket: c.DebugSocket}, slog.Default(), nil, nil)
	list, err := h.Debug().Sessions(ctx)
	if err != nil {
		return fmt.Errorf("debug socket %s: %w", c.DebugSocket, err)
	}
	fmt.Printf("debug socket ok: %d sessions\n", len(list))
	if _, err := os.Stat(c.MainSocket); err != nil {
		return fmt.Errorf("main socket %s: %w", c.MainSocket, err)
	}
	fmt.Printf("main socket ok: %s\n", c.MainSocket)
	return nil
}

func (cmd *ServeCmd) Run(cli *CLI) error {
	overrides := map[string]any{}
	if cmd.Listen != "" {
		overrides["listen"] = cmd.Listen
	}
	if cmd.DataDir != "" {
		overrides["data_dir"] = cmd.DataDir
	}
	c, err := loadConfig(cli, overrides)
	if err != nil {
		return err
	}
	log := newLogger(c)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(c.DataDir, "jcode-web.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	searchIndex := search.Open(ctx, filepath.Join(c.DataDir, "session-search.db"), c.Search, log)
	defer searchIndex.Close()

	pusher, err := push.New(ctx, log, st, c.PushSubject)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}

	h := hub.New(hub.Config{
		MainSocket:   c.MainSocket,
		DebugSocket:  c.DebugSocket,
		PollInterval: c.PollInterval,
		Retention:    c.Retention,
		AttachLinger: c.AttachLinger,
	}, log, st, pusher)
	h.SetSummarizer(summarize.New(summarize.Config{
		Enabled:         c.Summary.Enabled,
		BaseURL:         c.Summary.BaseURL,
		APIKey:          c.Summary.APIKey,
		Model:           c.Summary.Model,
		ReasoningEffort: c.Summary.ReasoningEffort,
		MaxTokens:       c.Summary.MaxTokens,
		Timeout:         c.Summary.Timeout,
	}))
	go h.Run(ctx)

	server := connect.NewServer(recoverInterceptor(log))
	jcodewebv1connect.RegisterSessionServiceHandler(server, &api.Sessions{Hub: h, Store: st, Search: searchIndex})
	jcodewebv1connect.RegisterPushServiceHandler(server, &api.Push{Pusher: pusher, Store: st})

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler { return http.NewCrossOriginProtection().Handler(next) })
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	rpc := http.NewServeMux()
	connecthttp.Mount(rpc, server)
	r.Handle("/"+jcodewebv1connect.SessionServiceName+"/*", rpc)
	r.Handle("/"+jcodewebv1connect.PushServiceName+"/*", rpc)
	r.Handle("/*", spa(web.FS()))

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Addr:              c.Listen,
		Handler:           r,
		Protocols:         p,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", c.Listen, "version", version, "data_dir", c.DataDir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// spa serves static files and falls back to index.html.
func spa(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			if _, err := fs.Stat(root, "index.html"); err != nil {
				http.Error(w, "frontend not built: run `make web`", http.StatusNotFound)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			name = "index.html"
		}
		switch {
		case name == "index.html" || name == "sw.js" || name == "manifest.webmanifest":
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(name, "assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func recoverInterceptor(log *slog.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
			defer func() {
				if r := recover(); r != nil {
					log.Error("rpc panic", "procedure", spec.Procedure, "panic", r)
					err = connect.NewError(connect.CodeInternal, "internal error")
				}
			}()
			return next(ctx, spec, stream)
		}
	}
}

func main() {
	var cli CLI
	kctx := kong.Parse(&cli,
		kong.Name("jcode-web"),
		kong.Description("Web UI to monitor and control jcode sessions."),
		kong.UsageOnError(),
	)
	kctx.FatalIfErrorf(kctx.Run(&cli))
}
