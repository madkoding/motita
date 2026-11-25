package gateway

import (
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/localmodel"
)

// localSummaryChoice is the local model as the model of the small background calls.
func localSummaryChoice() config.ModelChoice {
	return config.ModelChoice{Provider: config.ProviderLocal, Model: localmodel.ModelID}
}

// writeSummaryModel saves the models block with the summary kind set to choice and every other
// kind as it is.
func (s *Server) writeSummaryModel(choice config.ModelChoice) error {
	current := s.configuredModels()
	choices := make(map[string]config.ModelChoice, len(config.TaskKinds))
	for _, kind := range config.TaskKinds {
		choices[kind] = current.For(kind)
	}
	choices[config.TaskSummary] = choice
	return setTaskModels(s.opts.ConfigPath, choices)
}

// preferLocalForBackground gives the small background calls (titles, summaries, the audit) to the
// local model once it runs, so they cost no quota. It only fills a summary model that is empty: a
// model the person chose is never replaced.
func (s *Server) preferLocalForBackground() error {
	if !s.configuredModels().For(config.TaskSummary).Empty() {
		return nil
	}
	return s.writeSummaryModel(localSummaryChoice())
}

// releaseLocalBackground undoes it when the local model is deleted: a summary model that is the
// local one goes back to following the default.
func (s *Server) releaseLocalBackground() error {
	if s.configuredModels().For(config.TaskSummary) != localSummaryChoice() {
		return nil
	}
	return s.writeSummaryModel(config.ModelChoice{})
}
