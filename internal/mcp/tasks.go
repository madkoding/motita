package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// TASKS (experimental in revision 2025-11-25): a call the server answers at once with "received,
// I will tell you" while it works in the background. The caller keeps going and collects the
// result later, for a transcription, a deploy, an upload, any slow job.
//
// A task belongs to the connection that created it: a server started by this program stops when
// the run ends, and its unfinished tasks with it.

// Task statuses.
const (
	TaskWorking       = "working"
	TaskInputRequired = "input_required"
	TaskCompleted     = "completed"
	TaskFailed        = "failed"
	TaskCancelled     = "cancelled"
)

// Task is the state of one background call.
type Task struct {
	TaskID        string `json:"taskId"`
	Status        string `json:"status"`
	StatusMessage string `json:"statusMessage,omitempty"`
	// PollInterval is how many milliseconds the server suggests between looks.
	PollInterval int64 `json:"pollInterval,omitempty"`
}

// Done says whether the task will not change any more.
func (t Task) Done() bool {
	return t.Status == TaskCompleted || t.Status == TaskFailed || t.Status == TaskCancelled
}

// SupportsTasks says whether the server runs tool calls as tasks.
func (c *Client) SupportsTasks() bool {
	var caps struct {
		Tasks struct {
			Requests struct {
				Tools struct {
					Call *json.RawMessage `json:"call"`
				} `json:"tools"`
			} `json:"requests"`
		} `json:"tasks"`
	}
	_ = json.Unmarshal(c.Server.Capabilities, &caps)
	return caps.Tasks.Requests.Tools.Call != nil
}

// CallTask starts a tool as a background task and returns at once. ttl is how long the server
// should keep the result.
func (c *Client) CallTask(ctx context.Context, name string, args json.RawMessage, ttl time.Duration) (Task, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var res struct {
		Task Task `json:"task"`
	}
	err := c.Request(ctx, "tools/call", map[string]any{
		"name": name, "arguments": args, "task": map[string]any{"ttl": ttl.Milliseconds()},
	}, &res)
	if err == nil && res.Task.TaskID == "" {
		err = errors.New("the server did not return a task")
	}
	return res.Task, err
}

// Task asks for the state of a task.
func (c *Client) Task(ctx context.Context, id string) (Task, error) {
	var t Task
	err := c.Request(ctx, "tasks/get", map[string]any{"taskId": id}, &t)
	return t, err
}

// TaskResult is what the task's tool answered. The server holds the call until the task is over,
// so it is asked once Task says it is done.
func (c *Client) TaskResult(ctx context.Context, id string) (CallResult, error) {
	var res CallResult
	err := c.Request(ctx, "tasks/result", map[string]any{"taskId": id}, &res)
	return res, err
}

// CancelTask asks the server to stop a task.
func (c *Client) CancelTask(ctx context.Context, id string) (Task, error) {
	var t Task
	err := c.Request(ctx, "tasks/cancel", map[string]any{"taskId": id}, &t)
	return t, err
}
