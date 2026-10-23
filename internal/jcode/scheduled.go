package jcode

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type ScheduledTask struct {
	ID                    string    `json:"id"`
	ScheduledFor          time.Time `json:"scheduled_for"`
	Context               string    `json:"context"`
	TaskDescription       string    `json:"task_description"`
	Priority              string    `json:"priority"`
	TargetKind            string    `json:"target_kind"`
	TargetSessionID       string    `json:"target_session_id"`
	TargetParentSessionID string    `json:"target_parent_session_id"`
}

// ScheduledQueue reads the server's actual pending queue without changing it.
func (d *Debug) ScheduledQueue(ctx context.Context) ([]ScheduledTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := d.Run(ctx, "ambient:queue", "")
	if err != nil {
		return nil, err
	}
	var items []ScheduledTask
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return nil, fmt.Errorf("decode scheduled queue: %w", err)
	}
	for _, item := range items {
		if item.ScheduledFor.IsZero() {
			return nil, fmt.Errorf("scheduled task %q has no due time", item.ID)
		}
	}
	return items, nil
}

func (d *Debug) ScheduledTasks(ctx context.Context, sessionID string) ([]ScheduledTask, error) {
	items, err := d.ScheduledQueue(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]ScheduledTask, 0)
	for _, item := range items {
		if (item.TargetKind == "session" && item.TargetSessionID == sessionID) || (item.TargetKind == "spawn" && item.TargetParentSessionID == sessionID) {
			tasks = append(tasks, item)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ScheduledFor.Before(tasks[j].ScheduledFor) })
	return tasks, nil
}
