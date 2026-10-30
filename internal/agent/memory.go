package agent

import "github.com/madkoding/motita/internal/knowledge"

// saveLessons keeps what a task's report says it learned, for the tasks after it.
func (a *Agent) saveLessons(report *Report) {
	if report == nil {
		return
	}
	if err := knowledge.RecordLessons(a.cfg.Agent.WorkspaceDir, report.Lessons); err != nil {
		a.log.Warn("could not save what the task learned", "error", err)
	}
}
