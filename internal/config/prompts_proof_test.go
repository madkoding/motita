package config

import (
	"strings"
	"testing"
)

// TestThePromptsDemandProofOfNewBehaviour: an agent that writes a component and stops has done
// half the work. The execute prompt must make a test part of the change and forbid "done" before
// it was seen passing; the analysis must make the proof a success criterion, and the shipped
// procedure it points at must say the same.
func TestThePromptsDemandProofOfNewBehaviour(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"execute leaves a test":        {BaseExecuteTemplate.User, "LEAVE THE CHANGE PROVEN"},
		"execute runs it before done":  {BaseExecuteTemplate.User, "Only report \"done\": true after you have seen the new test pass"},
		"execute points at the skill":  {BaseExecuteTemplate.User, "verifying-a-change"},
		"execute commits are semantic": {BaseExecuteTemplate.User, "type(scope): description"},
		"execute links the pr":         {BaseExecuteTemplate.User, "[owner/repo#12](url)"},
		"execute watches the ci":       {BaseExecuteTemplate.User, "motita forge pr checks --wait --logs"},
		"analysis has a proof section": {BaseAnalyzeTemplate.User, "SUCCESS CRITERIA MUST BE PROVABLE"},
	} {
		if !strings.Contains(tc.text, tc.want) {
			t.Errorf("%s: the prompt must contain %q", name, tc.want)
		}
	}
}

// TestTheSynthesisPromptAsksForTheReportShape: the final answer is data a front end draws, so the
// prompt has to name every field the program decodes (see agent.Report).
func TestTheSynthesisPromptAsksForTheReportShape(t *testing.T) {
	for _, key := range []string{`"status"`, `"summary"`, `"changes"`, `"verification"`, `"risks"`, `"next_steps"`, `"evidence"`} {
		if !strings.Contains(BaseSynthesizeTemplate.User, key) {
			t.Errorf("the synthesis prompt must ask for %s", key)
		}
	}
}

// TestTheExecutePromptTellsTheAgentHowToGetToTheWriting: reported from a real session that read
// for thirteen rounds and wrote nothing. The prompt has to say that a change ends in files, that
// exploring is one round and not one file per round, and how to write.
func TestTheExecutePromptTellsTheAgentHowToGetToTheWriting(t *testing.T) {
	for _, want := range []string{
		"HOW A ROUND OF WORK GOES",
		"Explore in ONE round",
		"Then WRITE, in the next round",
		"heredoc",
		"every layer is done",
		"Reading is not progress by itself",
	} {
		if !strings.Contains(BaseExecuteTemplate.User, want) {
			t.Errorf("the execute prompt must contain %q", want)
		}
	}
}

// TestTheExecutePromptTeachesGettingToTheFinish: a real run read for thirteen rounds and wrote
// nothing. The prompt has to say what order the work goes in, on any project.
func TestTheExecutePromptTeachesGettingToTheFinish(t *testing.T) {
	for _, want := range []string{
		"GETTING TO THE FINISH, ON ANY PROJECT",
		"LOOK just enough",
		"WRITE in the same round",
		"Create a file with write_file and change one with edit_file",
		"no node_modules, no venv, no vendor",
		"RUN the project's own checks",
		"say in \"notes\" exactly what is left",
	} {
		if !strings.Contains(BaseExecuteTemplate.User, want) {
			t.Errorf("the execute prompt must contain %q", want)
		}
	}
}

// TestTheExecutePromptTeachesHowToCheck: a real run saw 22 server tests fail, called them
// pre-existing without comparing, and never tested the endpoint it had written. The prompt has to
// say how to test, how to read a failure, and what to do when the environment is what is broken.
func TestTheExecutePromptTeachesHowToCheck(t *testing.T) {
	for _, want := range []string{
		"HOW TO CHECK WHAT YOU WROTE, AND WHAT TO DO WHEN A CHECK FAILS",
		"copy its shape",
		"Run it by itself first",
		"the exit status you see is tail's",
		"echo \"exit=$?\"",
		"it is the next piece of work",
		"PROVE it instead of assuming it",
		"git stash",
		"Never leave new code untested because the old tests are red",
		"do not retry the same way more than twice",
		"a component test instead of a screenshot",
	} {
		if !strings.Contains(BaseExecuteTemplate.User, want) {
			t.Errorf("the execute prompt must contain %q", want)
		}
	}
}
