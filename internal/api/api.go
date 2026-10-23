// Package api implements the Connect services.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"

	v1 "github.com/elee1766/jcode-web/gen/jcodeweb/v1"
	"github.com/elee1766/jcode-web/gen/jcodeweb/v1/jcodewebv1connect"
	"github.com/elee1766/jcode-web/internal/hub"
	"github.com/elee1766/jcode-web/internal/jcode"
	"github.com/elee1766/jcode-web/internal/push"
	"github.com/elee1766/jcode-web/internal/store"
)

type Sessions struct {
	jcodewebv1connect.UnimplementedSessionServiceHandler
	Hub   *hub.Hub
	Store *store.Store
}

func (s *Sessions) CreateSession(ctx context.Context, req *v1.CreateSessionRequest) (*v1.CreateSessionResponse, error) {
	if req.WorkingDir == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "working_dir required")
	}
	id, name, err := s.Hub.CreateSession(ctx, req.WorkingDir)
	if err != nil {
		return nil, upstreamErr(err)
	}
	return &v1.CreateSessionResponse{SessionId: id, SessionName: name}, nil
}

func (s *Sessions) ListSessions(ctx context.Context, _ *v1.ListSessionsRequest) (*v1.ListSessionsResponse, error) {
	return &v1.ListSessionsResponse{Sessions: sessionsPB(s.Hub.Sessions())}, nil
}

func (s *Sessions) ListEvents(ctx context.Context, req *v1.ListEventsRequest) (*v1.ListEventsResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	evs, err := s.Store.EventsBeforeType(ctx, req.SessionId, req.EventType, req.BeforeId, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "list events").WithCause(err)
	}
	out := make([]*v1.Event, len(evs))
	for i := range evs {
		out[i] = eventPB(&evs[i])
	}
	return &v1.ListEventsResponse{Events: out}, nil
}

func (s *Sessions) Watch(ctx context.Context, req *v1.WatchRequest, stream jcodewebv1connect.SessionServiceWatchServerStream) error {
	// Subscribe before replay so nothing is missed between them.
	ch, unsub := s.Hub.Watch()
	defer unsub()

	conn := s.Hub.Connection()
	if err := stream.Send(&v1.WatchResponse{Update: &v1.WatchResponse_Connection{Connection: &v1.Connection{Connected: conn.Connected, Error: conn.Error}}}); err != nil {
		return err
	}
	if err := stream.Send(&v1.WatchResponse{Update: &v1.WatchResponse_Sessions{Sessions: &v1.SessionsSnapshot{Sessions: sessionsPB(s.Hub.Sessions())}}}); err != nil {
		return err
	}
	last := req.AfterId
	if last > 0 {
		for {
			evs, err := s.Store.EventsAfter(ctx, last, 500)
			if err != nil {
				return connect.NewError(connect.CodeInternal, "replay").WithCause(err)
			}
			for i := range evs {
				if err := stream.Send(&v1.WatchResponse{Update: &v1.WatchResponse_Event{Event: eventPB(&evs[i])}}); err != nil {
					return err
				}
				last = evs[i].ID
			}
			if len(evs) < 500 {
				break
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-ch:
			if !ok {
				return connect.NewError(connect.CodeResourceExhausted, "watch buffer full: reconnect to replay")
			}
			var msg *v1.WatchResponse
			switch {
			case u.Event != nil:
				if u.Event.ID <= last {
					continue
				}
				last = u.Event.ID
				msg = &v1.WatchResponse{Update: &v1.WatchResponse_Event{Event: eventPB(u.Event)}}
			case u.Sessions != nil:
				msg = &v1.WatchResponse{Update: &v1.WatchResponse_Sessions{Sessions: &v1.SessionsSnapshot{Sessions: sessionsPB(u.Sessions)}}}
			case u.Connection != nil:
				msg = &v1.WatchResponse{Update: &v1.WatchResponse_Connection{Connection: &v1.Connection{Connected: u.Connection.Connected, Error: u.Connection.Error}}}
			default:
				continue
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

func (s *Sessions) Attach(ctx context.Context, req *v1.AttachRequest, stream jcodewebv1connect.SessionServiceAttachServerStream) error {
	if req.SessionId == "" {
		return connect.NewError(connect.CodeInvalidArgument, "session_id required")
	}
	v, err := s.Hub.View(ctx, req.SessionId)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, err.Error())
	}
	defer v.Close()

	msgs := v.History
	if err := stream.Send(historyMsg(msgs, req.HistoryLimit)); err != nil {
		return err
	}
	for _, env := range v.Tail {
		if msg := attachPB(env); msg != nil {
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case env, ok := <-v.Events:
			if !ok {
				return stream.Send(&v1.AttachResponse{Update: &v1.AttachResponse_Detached{Detached: &v1.Detached{Reason: "upstream closed"}}})
			}
			if env.Type == "history" {
				var h jcode.History
				if json.Unmarshal(env.Raw, &h) == nil {
					if err := stream.Send(historyMsg(h.Messages, req.HistoryLimit)); err != nil {
						return err
					}
				}
				continue
			}
			msg := attachPB(env)
			if msg == nil {
				continue
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

func (s *Sessions) SendMessage(ctx context.Context, req *v1.SendMessageRequest) (*v1.SendMessageResponse, error) {
	if req.SessionId == "" || req.Content == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "session_id and content required")
	}
	mode, err := s.Hub.Send(ctx, req.SessionId, req.Content, hub.SendMode(req.Mode))
	if err != nil {
		return nil, upstreamErr(err)
	}
	return &v1.SendMessageResponse{Mode: v1.SendMode(mode)}, nil
}

func (s *Sessions) Cancel(ctx context.Context, req *v1.CancelRequest) (*v1.CancelResponse, error) {
	if err := s.Hub.Cancel(ctx, req.SessionId); err != nil {
		return nil, upstreamErr(err)
	}
	return &v1.CancelResponse{}, nil
}

func (s *Sessions) Btw(ctx context.Context, req *v1.BtwRequest) (*v1.BtwResponse, error) {
	if req.SessionId == "" || req.Question == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "session_id and question required")
	}
	id, name, err := s.Hub.Btw(ctx, req.SessionId, req.Question)
	if err != nil {
		if id != "" {
			return nil, upstreamErr(fmt.Errorf("fork %s (%s) was created, but the question was not confirmed: %w", name, id, err))
		}
		return nil, upstreamErr(err)
	}
	return &v1.BtwResponse{SessionId: id, SessionName: name}, nil
}

func (s *Sessions) Summarize(ctx context.Context, req *v1.SummarizeRequest) (*v1.SummarizeResponse, error) {
	if !s.Hub.SummariesEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "summaries are not configured")
	}
	sum, err := s.Hub.SummarizeSession(ctx, req.SessionId)
	if err != nil {
		return nil, upstreamErr(err)
	}
	return &v1.SummarizeResponse{Summary: sum.Text}, nil
}

func (s *Sessions) Overview(ctx context.Context, req *v1.OverviewRequest) (*v1.OverviewResponse, error) {
	if !s.Hub.SummariesEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "summaries are not configured")
	}
	mins := req.WindowMinutes
	if mins == 0 {
		mins = 60
	}
	text, err := s.Hub.Overview(ctx, time.Duration(mins)*time.Minute)
	if err != nil {
		return nil, upstreamErr(err)
	}
	return &v1.OverviewResponse{Text: text, Model: s.Hub.SummaryModel()}, nil
}

func (s *Sessions) GetFeatures(context.Context, *v1.GetFeaturesRequest) (*v1.GetFeaturesResponse, error) {
	return &v1.GetFeaturesResponse{Summaries: s.Hub.SummariesEnabled(), SummaryModel: s.Hub.SummaryModel()}, nil
}

func upstreamErr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return connect.NewError(connect.CodeUnavailable, err.Error())
}

type Push struct {
	jcodewebv1connect.UnimplementedPushServiceHandler
	Pusher *push.Pusher
	Store  *store.Store
}

func (p *Push) GetVapidKey(context.Context, *v1.GetVapidKeyRequest) (*v1.GetVapidKeyResponse, error) {
	return &v1.GetVapidKeyResponse{PublicKey: p.Pusher.PublicKey()}, nil
}

func (p *Push) Subscribe(ctx context.Context, req *v1.SubscribeRequest) (*v1.SubscribeResponse, error) {
	if req.Endpoint == "" || req.P256Dh == "" || req.Auth == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "endpoint, p256dh and auth required")
	}
	err := p.Store.UpsertPush(ctx, store.PushSubscription{Endpoint: req.Endpoint, P256dh: req.P256Dh, Auth: req.Auth, UserAgent: req.UserAgent})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "save subscription").WithCause(err)
	}
	return &v1.SubscribeResponse{}, nil
}

func (p *Push) Unsubscribe(ctx context.Context, req *v1.UnsubscribeRequest) (*v1.UnsubscribeResponse, error) {
	if err := p.Store.DeletePush(ctx, req.Endpoint); err != nil {
		return nil, connect.NewError(connect.CodeInternal, "delete subscription").WithCause(err)
	}
	return &v1.UnsubscribeResponse{}, nil
}

func (p *Push) Test(ctx context.Context, _ *v1.TestRequest) (*v1.TestResponse, error) {
	n := p.Pusher.Send(ctx, "jcode-web", "Test notification", "test", "/")
	return &v1.TestResponse{Sent: uint32(n)}, nil
}

func sessionsPB(in []hub.SessionView) []*v1.Session {
	out := make([]*v1.Session, len(in))
	for i, s := range in {
		detail := ""
		if s.Detail != nil {
			detail = *s.Detail
		}
		tu := &v1.TokenUsage{Input: s.TokenUsage.Input, Output: s.TokenUsage.Output}
		if s.TokenUsage.CacheRead != nil {
			tu.CacheRead = *s.TokenUsage.CacheRead
		}
		if s.TokenUsage.CacheWrite != nil {
			tu.CacheWrite = *s.TokenUsage.CacheWrite
		}
		out[i] = &v1.Session{
			Id: s.SessionID, Name: s.FriendlyName, Status: s.Status, IsProcessing: s.IsProcessing,
			Model: s.Model, Provider: s.Provider, WorkingDir: s.WorkingDir, SwarmId: s.SwarmID,
			ServerName: s.ServerName, Detail: detail, TokenUsage: tu, LastEventUnix: s.LastEventUnix,
			Attached: s.Attached, Summary: s.Summary, SummaryUnix: s.SummaryAt, ShortSummary: s.ShortSummary,
		}
	}
	return out
}

func eventPB(e *store.Event) *v1.Event {
	return &v1.Event{
		Id: e.ID, UpstreamId: e.UpstreamID, SessionId: e.SessionID, SessionName: e.SessionName,
		SwarmId: e.SwarmID, TimestampUnix: e.TimestampUnix, Type: e.Type, PayloadJson: e.PayloadJSON,
		Summary: e.Summary,
	}
}

func historyMsg(msgs []jcode.HistoryMessage, limit uint32) *v1.AttachResponse {
	total := len(msgs)
	if limit > 0 && len(msgs) > int(limit) {
		msgs = msgs[len(msgs)-int(limit):]
	}
	hist := &v1.History{Total: uint32(total), Messages: make([]*v1.HistoryMessage, len(msgs))}
	for i, m := range msgs {
		hist.Messages[i] = historyPB(m)
	}
	return &v1.AttachResponse{Update: &v1.AttachResponse_History{History: hist}}
}

func historyPB(m jcode.HistoryMessage) *v1.HistoryMessage {
	out := &v1.HistoryMessage{Role: m.Role, Content: m.Content}
	if m.ToolData != nil {
		out.Tool = &v1.ToolData{Id: m.ToolData.ID, Name: m.ToolData.Name, InputJson: string(m.ToolData.Input), Intent: m.ToolData.Intent}
	}
	return out
}

// attachPB maps an upstream event to the API. nil means skip.
func attachPB(env jcode.Envelope) *v1.AttachResponse {
	var f struct {
		Text       string  `json:"text"`
		ID         any     `json:"id"`
		Name       string  `json:"name"`
		Delta      string  `json:"delta"`
		Output     string  `json:"output"`
		Error      *string `json:"error"`
		Message    string  `json:"message"`
		Detail     string  `json:"detail"`
		Content    string  `json:"content"`
		StopReason *string `json:"stop_reason"`
	}
	_ = json.Unmarshal(env.Raw, &f)
	toolID, _ := f.ID.(string)
	r := &v1.AttachResponse{}
	switch env.Type {
	case "text_delta":
		r.Update = &v1.AttachResponse_TextDelta{TextDelta: &v1.TextDelta{Text: f.Text}}
	case "text_replace":
		r.Update = &v1.AttachResponse_TextReplace{TextReplace: &v1.TextDelta{Text: f.Text}}
	case "reasoning_delta":
		r.Update = &v1.AttachResponse_ReasoningDelta{ReasoningDelta: &v1.TextDelta{Text: f.Text}}
	case "tool_start":
		r.Update = &v1.AttachResponse_ToolStart{ToolStart: &v1.ToolStart{Id: toolID, Name: f.Name}}
	case "tool_exec":
		r.Update = &v1.AttachResponse_ToolExec{ToolExec: &v1.ToolStart{Id: toolID, Name: f.Name}}
	case "tool_input":
		r.Update = &v1.AttachResponse_ToolInput{ToolInput: &v1.ToolInput{Id: toolID, Delta: f.Delta}}
	case "tool_done":
		td := &v1.ToolDone{Id: toolID, Name: f.Name, Output: f.Output}
		if f.Error != nil {
			td.Error = *f.Error
		}
		r.Update = &v1.AttachResponse_ToolDone{ToolDone: td}
	case "message_end":
		td := &v1.TurnDone{}
		if f.StopReason != nil {
			td.StopReason = *f.StopReason
		}
		r.Update = &v1.AttachResponse_TurnDone{TurnDone: td}
	case "interrupted":
		r.Update = &v1.AttachResponse_Interrupted{Interrupted: &v1.Interrupted{}}
	case "error":
		r.Update = &v1.AttachResponse_Error{Error: &v1.UpstreamError{Message: f.Message}}
	case "status_detail":
		r.Update = &v1.AttachResponse_StatusDetail{StatusDetail: &v1.StatusDetail{Detail: f.Detail}}
	case "tokens":
		var t struct {
			Input      uint64  `json:"input"`
			Output     uint64  `json:"output"`
			CacheRead  *uint64 `json:"cache_read_input"`
			CacheWrite *uint64 `json:"cache_creation_input"`
		}
		_ = json.Unmarshal(env.Raw, &t)
		tu := &v1.TokenUsage{Input: t.Input, Output: t.Output}
		if t.CacheRead != nil {
			tu.CacheRead = *t.CacheRead
		}
		if t.CacheWrite != nil {
			tu.CacheWrite = *t.CacheWrite
		}
		r.Update = &v1.AttachResponse_Tokens{Tokens: tu}
	case "peer_user_message":
		r.Update = &v1.AttachResponse_UserMessage{UserMessage: &v1.UserMessage{Content: f.Content, Source: "peer"}}
	case "soft_interrupt_injected":
		r.Update = &v1.AttachResponse_UserMessage{UserMessage: &v1.UserMessage{Content: f.Content, Source: "interrupt"}}
	case "web_user_message":
		r.Update = &v1.AttachResponse_UserMessage{UserMessage: &v1.UserMessage{Content: f.Content, Source: "web"}}
	case "history", "ack", "pong", "mcp_status", "done", "session", "kv_cache_request", "connection_type",
		"connection_phase", "available_models_updated", "swarm_status", "model_catalog":
		return nil
	default:
		r.Update = &v1.AttachResponse_Raw{Raw: &v1.RawEvent{Type: env.Type, Json: string(env.Raw)}}
	}
	return r
}
