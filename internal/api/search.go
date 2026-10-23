package api

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	v1 "github.com/elee1766/jcode-web/gen/jcodeweb/v1"
	"github.com/elee1766/jcode-web/internal/search"
)

func (s *Sessions) GetSearchIndexStatus(_ context.Context, _ *v1.GetSearchIndexStatusRequest) (*v1.GetSearchIndexStatusResponse, error) {
	if s.Search == nil {
		return &v1.GetSearchIndexStatusResponse{Status: &v1.SearchIndexStatus{Error: "session search unavailable"}}, nil
	}
	return &v1.GetSearchIndexStatusResponse{Status: s.Search.Status()}, nil
}
func (s *Sessions) SearchSessions(ctx context.Context, req *v1.SearchSessionsRequest) (*v1.SearchSessionsResponse, error) {
	if s.Search == nil {
		return &v1.SearchSessionsResponse{Status: &v1.SearchIndexStatus{Error: "session search unavailable"}}, nil
	}
	out, err := s.Search.Search(ctx, req.Query, req.Limit, req.SessionId)
	if errors.Is(err, search.ErrQuery) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, connect.NewError(connect.CodeDeadlineExceeded, "session search timed out").WithCause(err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, connect.NewError(connect.CodeCanceled, "session search canceled").WithCause(err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, "session search failed").WithCause(err)
	}
	if s.Hub != nil {
		live := sessionsPB(s.Hub.Sessions())
		byID := make(map[string]*v1.Session, len(live))
		for _, session := range live {
			byID[session.Id] = session
		}
		for _, result := range out.Results {
			if session := byID[result.Session.Id]; session != nil {
				saved := result.Session
				result.Session = session
				if session.Name == "" {
					session.Name = saved.Name
				}
				if session.WorkingDir == "" {
					session.WorkingDir = saved.WorkingDir
				}
				if session.Model == "" {
					session.Model = saved.Model
				}
				if session.ParentSessionId == "" {
					session.ParentSessionId = saved.ParentSessionId
				}
			}
		}
	}
	return out, nil
}
func (s *Sessions) liveSession(id string) bool {
	if s.Hub == nil {
		return false
	}
	for _, session := range s.Hub.Sessions() {
		if session.SessionID == id {
			return true
		}
	}
	return false
}
