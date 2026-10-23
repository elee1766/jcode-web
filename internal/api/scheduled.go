package api

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	v1 "github.com/elee1766/jcode-web/gen/jcodeweb/v1"
)

func (s *Sessions) ListScheduledTasks(ctx context.Context, req *v1.ListScheduledTasksRequest) (*v1.ListScheduledTasksResponse, error) {
	if strings.TrimSpace(req.SessionId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "session_id required")
	}
	tasks, err := s.Hub.Debug().ScheduledTasks(ctx, req.SessionId)
	if err != nil {
		return nil, upstreamErr(err)
	}
	out := &v1.ListScheduledTasksResponse{}
	for _, task := range tasks {
		text := task.TaskDescription
		if text == "" {
			text = task.Context
		}
		out.Tasks = append(out.Tasks, &v1.ScheduledTask{Id: task.ID, Task: text, ScheduledFor: task.ScheduledFor.Format(time.RFC3339Nano), Target: task.TargetKind, Priority: task.Priority})
	}
	return out, nil
}
