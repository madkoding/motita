package agent

import "github.com/madkoding/motita/internal/knowledge"

// saveLessons keeps what a task's report says it learned, for the tasks after it, and mirrors the
// new lessons into the person's memory so it grows without the model choosing to save.
func (a *Agent) saveLessons(report *Report) {
	if report == nil {
		return
	}
	if err := knowledge.KeepLessons(a.memory, a.cfg.Agent.WorkspaceDir, report.Lessons); err != nil {
		a.log.Warn("could not save what the task learned", "error", err)
	}
}
