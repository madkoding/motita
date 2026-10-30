package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/task"
)

// taskRun is one run of the 9-step loop: the task, what the phases decided about it, and the
// state the execution rounds carry from one to the next.
type taskRun struct {
	a      *Agent
	ctx    context.Context
	t      task.Task
	depth  int
	prefix string
	start  time.Time
	// lastDoing is what the previous round's actions were for, in words.
	lastDoing string
	res       TaskResult

	analysis Analysis
	plan     Plan
	// proceededOnAssumption records the reading the agent adopted when it had nobody to ask, so
	// the final report can say what it assumed instead of presenting the result as unqualified.
	proceededOnAssumption string

	// The execution cycle: see execute for what each of these is.
	journal        []roundRecord
	mem            *workMemory
	baseline       string
	baselineOK     bool
	lastSources    string
	before         *runBaseline
	readOnlyRounds int
	// singleActionRounds counts the progress rounds in a row that proposed exactly one action.
	singleActionRounds int
	// singleActionTotal counts every progress round that proposed exactly one action.
	singleActionTotal int
	unbackedDone      int
	verifyChallenges  int
	auditChallenges   int
	auditPassed       bool
	toolingRetries    int
	trail             []string
	workLog           workLog
	rejected          int
	unusable          int
	repeats           int
	lastSignature     string
	maxSteps          int
	round             int
}

// loop is the 9-step loop itself.
func (a *Agent) loop(ctx context.Context, t task.Task, depth int) TaskResult {
	r := &taskRun{a: a, ctx: ctx, t: t, depth: depth, start: time.Now(), prefix: strings.Repeat("  ", depth)}
	r.res = TaskResult{Task: t.Description, Attempts: 0}
	a.log.Info(r.prefix+"task received", "origin", t.Origin, "depth", depth,
		"description", truncate(t.Description, 200))

	// [2] Extract context and success criteria: the statement of the validation
	// rules shown to the LLM is built here, so it knows what it will be measured
	// against.
	rules := a.describeRules()

	// [3] Analysis.
	a.report("analysing the task...")
	r.analysis = a.analysisPhase(ctx, t, rules, depth)

	// A SUBTASK is work by construction: the plan that declared it already decided there is
	// something to do. It must not be answered as chat - that counted as a PASS for work never
	// done - and it cannot stop to ask: its question would surface as the parent's failure
	// ("only 0 of 3 subtasks passed") with the question itself lost, because the user is
	// talking to the PARENT turn. It proceeds on the reading the analysis stated instead.
	//
	// The turn that applies suggestions the user already accepted is the same: the list IS the
	// request, and asking what it meant sends the user back to a decision they just made.
	//
	// The turn that carries out a plan the person just approved is work by the same token, and
	// the plan it carries is the plan: see followedPlan below.
	_, following := FollowedPlan(t.Description)
	if depth > 0 || IsApplyingTurn(t.Description) || following {
		r.workAsSubtask()
	}

	if res, done := r.answerOrAsk(); done {
		return res
	}
	a.report("understood: %s", r.analysis.Summary)
	if len(r.analysis.SuccessCrit) > 0 {
		a.report("%sdone means:\n- %s", ThinkingPrefix, strings.Join(r.analysis.SuccessCrit, "\n- "))
	}

	// [4] Plan. A plan the person already approved is not planned again, and one the analysis
	// wrote itself is taken as it is: the plan phase is for work that splits into subtasks.
	switch {
	case following:
		r.plan = approvedPlan()
		a.report("carrying out the approved plan")
	case r.quickPlan():
		r.plan = planFromAnalysis(r.analysis)
		a.report("plan ready: %d steps", len(r.plan.Steps))
	default:
		a.report("planning...")
		r.plan = a.planPhase(ctx, t, r.analysis, depth)
		a.report("plan ready: %d steps", len(r.plan.Steps))
	}
	if len(r.plan.Steps) > 0 {
		a.report("%splan:\n%s", ThinkingPrefix, strings.TrimRight(describePlan(r.plan), "\n"))
	}

	// [5] Splitting into subtasks: each one re-enters the same flow, one level
	// further down. The limit is respected to avoid infinite recursion.
	//
	// The PLAN is what decides. It used to require a second vote from the analysis phase
	// (`analysis.NeedsSubtasks`), and when the two disagreed the plan's own subtasks were
	// silently dropped: measured in a real run, `plan generated` reported five subtasks and
	// the line below never executed, so five declared pieces of work were thrown away.
	// A model that wrote down subtasks has said there are subtasks; asking it twice only
	// adds a way to lose them.
	//
	// Blank entries are dropped BEFORE deciding: a plan whose subtasks are all blank has no
	// subtasks, and it used to end as "only 0 of 0 subtasks passed validation" - a failure
	// for a task nobody ever tried.
	if subs := nonBlank(r.plan.Subtasks); len(subs) > 0 {
		switch {
		case depth >= a.cfg.Agent.SubtaskDepth:
			// The limit is reached: the warning below, then the task runs as one.
		case a.parallelSubtasks(ctx, r.plan, subs):
			return a.runSubtasksParallel(ctx, t, r.analysis, subs, depth, r.start, r.prefix)
		default:
			return a.runSubtasks(ctx, t, r.analysis, subs, depth, r.start, r.prefix)
		}
		a.log.Warn(r.prefix+"subtask splitting reached the configured limit; continuing as a single task",
			"depth", depth, "limit", a.cfg.Agent.SubtaskDepth,
			"subtasks", len(subs))
	}

	return r.execute()
}

// workAsSubtask makes the analysis of a subtask describe work (see loop).
func (r *taskRun) workAsSubtask() {
	an := &r.analysis
	if an.resolveKind() != KindTask || !an.Understandable || an.Question != "" {
		r.a.log.Info(r.prefix+"this turn cannot chat or ask: proceeding as work",
			"kind", an.Kind, "question", truncate(an.Question, 160))
		if assumed := cleanAssumption(an.Assumption); assumed != "" {
			r.proceededOnAssumption = assumed
		}
	}
	an.Kind = KindTask
	an.Understandable = true
	an.Question = ""
	an.Questions = nil
}

// answerOrAsk ends the turn when there is no work to run yet: a message that is not a task is
// answered, and a request that cannot be read well enough is asked about. done reports that the
// turn ended, with res as its result.
func (r *taskRun) answerOrAsk() (res TaskResult, done bool) {
	// The message is not always a task. A greeting, a question about what just happened, or
	// thinking out loud has nothing to do, and running the loop for it would plan work nobody
	// asked for and report a result nobody read.
	//
	// This is checked FIRST, before the ask/assume logic below, because it is a different
	// question: "is there work here?" comes before "can I read the work well enough?".
	switch r.analysis.resolveKind() {
	case KindChat:
		return r.chatReply(), true
	case KindAsk:
		// Forced into the asking path even when the model left "understandable" true: the kind
		// is the newer signal and saying "ask" is unambiguous.
		r.analysis.Understandable = false
	default:
		// Work: whether it can be read well enough is decided below.
	}

	if r.analysis.Understandable && r.analysis.Question == "" {
		return TaskResult{}, false
	}
	return r.askOrAssume()
}

// chatReply answers a message that has nothing to run.
func (r *taskRun) chatReply() TaskResult {
	reply := strings.TrimSpace(r.analysis.Reply)
	if reply == "" {
		// The model classified it as chat but wrote nothing. Saying so is better than
		// falling through to the loop: the classification is still the model's judgement,
		// and a task plan is the one answer the user did not ask for.
		//
		// The line is in English because everything else the interface says is: the
		// prompts, the help screen, the errors. One sentence in another language would
		// be the only one of its kind, and there is no language setting to make it
		// correct for the user reading it.
		reply = "There is nothing to run in your message."
	}
	r.res.Kind = KindChat
	r.res.Reply = reply
	r.res.Pass = true
	r.res.Reason = "conversational reply"
	r.res.DurationMS = time.Since(r.start).Milliseconds()
	// The reply is the answer, delivered with the result: reported as progress too, an interface
	// filed it as a step of the turn as well, and showed the answer twice.
	r.a.converse(r.t, reply)
	r.a.log.Info(r.prefix+"answered as chat", "reply", truncate(reply, 200))
	return r.res
}

// askOrAssume handles a request that could not be read well enough to act on. ASK, do not
// refuse. done is false when the run proceeds on the stated assumption instead.
//
// This used to end the turn with "the task was declared not understandable", which throws
// away everything the user typed and makes them write it again — for a request they can
// usually clarify in three words. A user mistypes, abbreviates, and omits what they think
// is obvious; the agent's job is to close that gap, not to report it.
//
// The question goes back through the interface as a normal result, so the user sees it in
// the conversation and answers with their next message. The assumption travels with it:
// it is what the agent would do without an answer, which lets the user confirm with a
// single word, and it is also the honest fallback if the interface has nobody to ask.
func (r *taskRun) askOrAssume() (res TaskResult, done bool) {
	analysis := r.analysis
	r.res.NeedsInput = true
	r.res.Question = strings.TrimSpace(analysis.Question)
	// Cleaned here as well as in the question, because this field is the one summarise
	// prints its own "If you do not tell me otherwise, I will assume:" in front of. Left
	// raw it produced the stutter the window showed. spanish-fixture: the model emitted
	// spanish-fixture: the run produced the lead twice over, and it has to come out once:
	// spanish-fixture: "Si no me dices otra cosa, asumiré: Si no me dices otra cosa,
	// spanish-fixture: reviso el proyecto".
	// The two paths (the single question and
	// the list) both need the cleaned form, and this is the one that reaches the chat.
	r.res.Assumption = cleanAssumption(analysis.Assumption)
	r.res.Options = cleanOptions(analysis.Options)
	r.res.Questions = buildQuestions(analysis)
	r.res.Reason = strings.Join(analysis.Risks, "; ")
	r.res.DurationMS = time.Since(r.start).Milliseconds()
	r.fillFromQuestions()

	// Nothing to ask AND nothing to assume is a genuine dead end, and it stays a failure.
	//
	// The difference is worth being precise about. A QUESTION is a request the agent can
	// name and the user can answer; an ASSUMPTION is a reading the agent can act on. With
	// neither, there is nothing to ask and nothing to do, and inventing a generic "what did
	// you mean?" would be asking the user to repeat what they just said — the report is
	// clearer, and a batch run still exits non-zero for cron to notice.
	if r.res.Question == "" && r.res.Assumption == "" {
		r.res.NeedsInput = false
		r.res.Question = ""
		r.res.Reason = strings.Join(analysis.Risks, "; ")
		r.a.report("task not understandable: %s", r.res.Reason)
		return r.res, true
	}
	if r.res.Question == "" {
		// The model named an assumption but no question. Asking is still right — the user can
		// confirm or correct the reading in one word — so the question is derived from it.
		r.res.Question = "Am I on the right track? If not, tell me exactly what you want."
	}

	// Nobody to ask: proceed on the assumption instead of stalling.
	//
	// A batch run has no user at the other end, so a question would wait forever. The
	// assumption the model itself proposed is the reading to act on — and acting on a stated
	// assumption is what an engineer does when the ticket is thin, not refusing to work.
	if !r.a.Interactive && r.res.Assumption != "" {
		r.proceedOnAssumption()
		return TaskResult{}, false
	}
	if r.shouldAssume() {
		r.assumeAndGo()
		return TaskResult{}, false
	}
	r.askUser()
	return r.res, true
}

// shouldAssume says whether a question with a stated assumption is answered by the agent itself:
// a person is there, the setting is on, and the request does not carry several independent gaps.
func (r *taskRun) shouldAssume() bool {
	return r.a.cfg.Agent.AssumeFirst && r.res.Assumption != "" &&
		len(r.res.Questions) < 2
}

// assumeAndGo proceeds on the assumption the analysis stated, with a person who could have been
// asked: the report says what was assumed, so they correct it with the next message instead of
// answering a question before any work starts.
func (r *taskRun) assumeAndGo() {
	assumed := r.res.Assumption
	r.a.log.Info("assuming instead of asking", "question", truncate(r.res.Question, 160),
		"assumption", truncate(assumed, 200))
	r.a.report("assuming: %s", assumed)
	r.analysis.Understandable = true
	r.analysis.Question = ""
	r.res.NeedsInput = false
	r.res.Question = ""
	r.res.Options = nil
	r.res.Questions = nil
	r.res.Assumption = ""
	r.proceededOnAssumption = assumed
}

// fillFromQuestions derives the single question fields from the list when they are empty.
//
// A turn may report its gap in EITHER shape — the single fields or the list — and the
// rest of this function reads the single fields. Left alone, a turn that used the list
// looked like it had asked nothing: question empty, assumption empty, and the dead-end
// check below failed a turn that had two questions ready to show. That is what the
// hardware run reported as "task not understandable:" followed by "failed:", and it is
// the failure mode the list form makes common, because the prompt tells the model to use
// it whenever a request has several gaps.
//
// So the single fields are derived from the list when they are empty, rather than the
// list being a second, parallel path. One source of truth, and every reader below keeps
// working whether the model answered in one shape or the other.
func (r *taskRun) fillFromQuestions() {
	if len(r.res.Questions) == 0 {
		return
	}
	if r.res.Question == "" {
		r.res.Question = r.res.Questions[0].Text
	}
	if r.res.Assumption == "" {
		r.res.Assumption = r.res.Questions[0].Assumption
	}
}

// proceedOnAssumption takes the stated assumption as the reading to work on, when there is
// nobody to ask.
func (r *taskRun) proceedOnAssumption() {
	r.a.log.Info("no user to ask: proceeding on the stated assumption",
		"question", truncate(r.res.Question, 160), "assumption", truncate(r.res.Assumption, 200))
	r.a.report("no user to ask; assuming: %s", r.res.Assumption)
	r.analysis.Understandable = true
	r.analysis.Question = ""
	// No fallback for an empty summary is needed: analysisPhase already replaced it with
	// the task description when the model left it blank, so by this point it always has
	// something. A branch here would be unreachable.
	//
	// The result describes the run that is now PROCEEDING, not the question that was
	// skipped: leaving these set would report a finished run as one that is still waiting
	// for an answer. They are re-applied below if the run later fails.
	r.res.NeedsInput = false
	r.res.Question = ""
	r.res.Assumption = ""
	r.proceededOnAssumption = r.analysis.Summary
}

// askUser puts the question to the user and records it in the conversation.
func (r *taskRun) askUser() {
	// The phase line carries the question itself, in the user's language (the model
	// writes it), so no English label is put in front of it. spanish-fixture: putting
	// spanish-fixture: an English label in front of the question a model emitted,
	// spanish-fixture: "¿qué carpeta?", mixed the two languages in one line and read
	// like a system error rather than the agent asking something.
	// mixed two languages in one line and read like a system error rather than the
	// agent asking something.
	r.a.report("%s", r.res.Question)
	r.a.log.Info("asking the user instead of guessing", "question", truncate(r.res.Question, 200),
		"assumption", truncate(r.res.Assumption, 200))
	// The question is recorded in the conversation. Their next message answers it, and
	// without this the answer arrives with nothing to refer to — which is exactly the
	// amnesia that makes a clarifying question useless.
	said := r.res.Question
	if r.res.Assumption != "" {
		// Printed with no lead of the interface's own, for the same reason as in the
		// TUI: the assumption is already a sentence in the user's language, and a
		// hardcoded lead would fix the conversation's language from the program.
		said += "\n\n" + r.res.Assumption
	}
	// Several questions are recorded whole. With only the first one kept, answers to the others
	// arrived on the next turn with nothing in the conversation to pair them with.
	if listed := AskedText(r.res.Questions); listed != "" {
		said = listed
	}
	r.a.note(r.t, said, KindAsk)
}

// execute is steps [6]-[9]: the execution and validation cycle.
//
// THE LOOP CONTRACT. Every round ends in exactly one of these, and each has its own bound:
//
//   - PROGRESS: the model ran actions and reports work left ("done": false). It is work,
//     not a failure, so it spends only the step budget. The anchor does NOT run on these
//     rounds: the state halfway through a change routinely breaks the project's gate (a
//     renamed function before its callers are updated), and charging that to max_retries
//     killed multi-step tasks at the fourth intermediate round. It also cost the whole
//     gate - measured at ~45 s on a real project - on every round of a long task.
//   - CLAIM: the model reports the task done. The anchor runs, always, and it alone
//     decides: PASS ends the task, a refusal is a REJECTED claim, and those are what
//     max_retries bounds.
//   - UNUSABLE: the reply could not be used (malformed JSON, or nothing to run while
//     claiming work is left). One bad reply used to end the whole task; it is now
//     told to the model, and only maxUnusableReplies IN A ROW end the run.
//   - STALL: a progress round identical to the one before it, same actions and same
//     output. The model is warned, and a run that keeps repeating itself is stopped
//     instead of spending the rest of the budget going nowhere.
//
// Every round is recorded in the journal the next round reads, labelled with what it
// WAS. It used to be labelled "failed attempt" whatever happened, so a model that made
// progress was told it had failed and redid the work.
func (r *taskRun) execute() TaskResult {
	a := r.a
	r.journal = []roundRecord{}
	// mem is what the run remembers besides the journal: the whole output of every read
	// still valid, and the model's own notes. See workmemory.go for why the journal alone
	// made a run re-read the same files a hundred times.
	r.mem = &workMemory{}
	// The sources as the run found them. It is what a claim of "done" is measured against: a
	// project's own checks are green on a tree nobody touched, so the anchor alone cannot tell
	// "finished" from "never started". See tree.go.
	r.baseline, r.baselineOK = treeFingerprint(a.cfg.Agent.WorkspaceDir, true)
	r.lastSources = r.baseline
	// The same tree as a commit, for the anchor: a check that fails on a claim is run again on
	// it to tell a failure the run caused from one that was already there. See baseline.go.
	r.before = a.startBaseline(r.ctx)
	defer r.before.close(r.ctx)
	// The project's gate as the task found it: a PASS from a gate the run itself rewrote is not
	// taken without the user's word. A background agent keeps the one its main agent started with.
	if !a.isChild || a.gate == "" {
		a.gate, a.gateTests = a.gateFingerprint(), max(a.testCount(), a.gateTests)
	}
	defer r.logRoundSummary()
	r.resume()
	// workLog is what every round ran and printed, for the final answer: the synthesis used
	// to see only the LAST round, which on a long task is a final `git status` and nothing
	// of the work.
	r.maxSteps = a.maxSteps()
	// A message typed while this task runs steers it (see steer.go); only the outermost task
	// reads them, since the person talks to the turn, not to a subtask.
	if r.depth == 0 {
		a.steer.begin()
		defer a.steer.end()
	}
	for {
		if res, done := r.step(); done {
			return res
		}
	}
}

// resume starts the journal of a run that RESUMES after an interruption (a restart, a crash)
// with the work its earlier life recorded, instead of an empty journal. The files it changed are
// on disk, but the reasons and the order are not: without this the model reads the request as
// new, re-reads the project and rebuilds what it already built.
func (r *taskRun) resume() {
	prior := r.a.workSoFar(r.t)
	if prior == "" {
		return
	}
	r.trail = []string{prior}
	r.journal = append(r.journal, roundRecord{
		round: 0, kind: roundProgress, commands: "(before the interruption)",
		detail: "This task was already being worked on when the run was interrupted. What " +
			"it had done, oldest first:\n" + prior + "\nThe changes are in the working " +
			"directory. Check it with `git status` and `git diff` FIRST, then continue " +
			"with what is left: do not start over.",
	})
}

// step runs one round. done reports that the run ended, with res as its result.
func (r *taskRun) step() (res TaskResult, done bool) {
	if !r.nextRound() {
		return r.res, true
	}
	r.res.Attempts = r.round
	r.a.noteRound(r.round)
	r.absorbSteering()

	// [6] The LLM proposes the concrete action.
	action, usable, stop := r.decide()
	if stop {
		return r.res, true
	}
	if !usable {
		return TaskResult{}, false
	}

	// [7] Run in the sandbox.
	runOutput, runErr := r.runActions(action)

	if !action.isDone() {
		return r.progress(action, runOutput, runErr)
	}
	r.repeats, r.lastSignature = 0, ""

	if r.claimSentBack(action) {
		return TaskResult{}, false
	}

	// [8] The model claims the task is done: validate with the anchor, always.
	r.a.report("validating with anchor...")
	validation := r.a.validateClaim(r.ctx, r.before)
	r.res.Validation = &validation

	if validation.Pass && runErr == nil {
		// [9] Validation PASS and the model reports the task finished: now the final action.
		return r.finish(action, validation)
	}
	return r.rejectClaim(action, runOutput, runErr, validation)
}

// nextRound opens the next round. It is false when the run must end here, with res saying why.
//
// The budget is a CHECKPOINT, not a wall. Measured on a real request: the agent was
// still working when 24 rounds ran out, and the run ended with "the task is not
// finished" and nothing else - the user had to start over and hope the next attempt
// got further. The work it had done was in the checkout; the DECISION left to the
// user was simply never offered.
//
// So when the budget runs out with work left, the loop asks. Yes means another
// maxSteps rounds on the SAME run, continuing from everything already done, which
// is the difference between "keep going" and "start again".
//
// The number of times it may ask is not bounded here on purpose: each ask is a
// human answering, and a human who keeps saying yes is not a runaway loop.
func (r *taskRun) nextRound() bool {
	a := r.a
	if rounded := r.round / r.maxSteps; rounded > 0 && r.round%r.maxSteps == 0 {
		// One full budget has been spent. Ask before spending another.
		cont, err := a.continueBudget(r.ctx, r.round, r.maxSteps, r.prefix)
		if err != nil || !cont {
			r.res.Reason = fmt.Sprintf(
				"the task is not finished: %d rounds were used and the model still reports work left. "+
					"Raise agent.max_steps, or split the request into smaller tasks", r.round)
			r.res.DurationMS = time.Since(r.start).Milliseconds()
			a.report("%s", r.res.Reason)
			a.log.Warn(r.prefix+"the step budget ran out and the user did not continue",
				"max_steps", r.maxSteps, "rounds", r.round)
			a.escalate(r.ctx, r.prefix)
			return false
		}
		a.report("continuing with another %d rounds...", r.maxSteps)
	}
	r.round++
	if r.round > r.maxSteps*maxBudgetExtensions {
		// A ceiling on the ceiling. A gateway whose approver answers "yes" without a
		// human (a script, a test) would otherwise loop for ever; the limit is high
		// enough that no real session reaches it, because reaching it means 100
		// answered questions.
		r.res.Reason = fmt.Sprintf(
			"the task is not finished after %d rounds and %d continuations, so this run stops here. "+
				"Split the request into smaller tasks", r.round-1, maxBudgetExtensions-1)
		r.res.DurationMS = time.Since(r.start).Milliseconds()
		a.report("%s", r.res.Reason)
		a.escalate(r.ctx, r.prefix)
		return false
	}
	return true
}

// Lines the loop reports while it chooses the next action. They say what the agent is working on
// rather than a round counter, which told the reader nothing about the work.
const (
	decidingFirst    = "deciding the first step..."
	decidingNext     = "deciding the next step..."
	startingWith     = "starting with: "
	decidingNextWith = "deciding the next step after: "
	// focusWidth keeps the activity line to one line.
	focusWidth = 80
)

// decidingLine is the progress line of a round that is about to ask for its action: the plan's
// first step on the first round, and on the later ones what the previous round did, in the words
// its actions announced themselves with (never the command, which told the reader nothing).
func (r *taskRun) decidingLine() string {
	if len(r.journal) > 0 {
		if r.lastDoing != "" {
			return decidingNextWith + truncate(r.lastDoing, focusWidth)
		}
		return decidingNext
	}
	if len(r.plan.Steps) > 0 {
		if first := collapse(r.plan.Steps[0].Action); first != "" {
			return startingWith + truncate(first, focusWidth)
		}
	}
	return decidingFirst
}

// decide asks the model for this round's action. usable is false when the reply could not be
// used and the round is to be asked again; stop ends the run.
func (r *taskRun) decide() (action Action, usable, stop bool) {
	a := r.a
	a.report("%s", r.decidingLine())
	action, err := r.actionPhase()
	if err == nil && len(action.Actions) == 0 && !action.isDone() {
		// Nothing to run while claiming there is work left is not a round of work: it is
		// a reply the loop cannot act on, and the model is told so rather than being
		// credited with progress it did not make.
		err = errors.New(`the reply proposed no actions but reported "done": false; ` +
			`propose the next actions, or report "done": true if the task is complete`)
	}
	if err != nil {
		return action, false, r.unusableReply(err)
	}
	r.unusable = 0
	// The model's reasoning for this round, whole. It used to be cut at 120 characters,
	// which is where the reasoning usually starts to say something.
	if reasoning := strings.TrimSpace(action.Reasoning); reasoning != "" {
		a.report("%s%s", ThinkingPrefix, reasoning)
	}
	return action, true, false
}

// unusableReply records a reply the round could not use. It reports whether the run ends.
func (r *taskRun) unusableReply(err error) bool {
	a := r.a
	if r.ctx.Err() != nil {
		r.res.Reason = "the run was cancelled"
		r.res.DurationMS = time.Since(r.start).Milliseconds()
		return true
	}
	r.unusable++
	a.log.Warn(r.prefix+"the action phase produced nothing usable",
		"round", r.round, "in_a_row", r.unusable, "error", err.Error())
	if r.unusable >= maxUnusableReplies {
		// The CAUSE is carried, and it is the LLM's own message. The old prefix said
		// "could not obtain the action from the LLM" and left the reader to guess: the
		// action was not missing, the reply arrived shaped in a way this phase cannot
		// use, and the message below now says which shape and which tool.
		r.res.Reason = "could not obtain the action from the LLM: " + err.Error()
		r.res.DurationMS = time.Since(r.start).Milliseconds()
		a.report("failed to get an action: %v", err)
		a.escalate(r.ctx, r.prefix)
		return true
	}
	a.report("the reply could not be used, asking again: %s", truncate(err.Error(), 160))
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundUnusable, commands: "(no usable reply)",
		detail: "The reply could not be used: " + truncate(err.Error(), 1000) +
			"\nAnswer with the JSON object exactly as the ACTION section shows it.",
	})
	return false
}

// runActions runs the round's actions in the sandbox and adds what they printed to the work log.
func (r *taskRun) runActions(action Action) (string, error) {
	r.mem.setNotes(action.Notes)
	r.mem.executed, r.mem.recalled = 0, 0
	runOutput, runErr := r.a.runRound(r.ctx, action.Actions, r.prefix, r.mem, r.round)
	// The background agents that finished since the last round report here, so the model reads
	// their results without having to ask for them.
	if reports := r.a.takeReports(nil); reports != "" {
		runOutput += reports
	}
	if strings.TrimSpace(runOutput) != "" {
		fmt.Fprintf(&r.workLog, "## Round %d\n%s\n", r.round, runOutput)
	}
	return runOutput, runErr
}

// progress records a PROGRESS round. The anchor is not consulted - see the loop contract in
// execute - and nothing is charged to max_retries. The round is recorded so the next one
// continues from it instead of proposing the same first step again.
func (r *taskRun) progress(action Action, runOutput string, runErr error) (res TaskResult, done bool) {
	a := r.a
	detail := "Actions:\n" + commandList(action) + "\nOutput:\n" + truncateMiddle(runOutput, roundDetailOutput)
	if runErr != nil {
		detail += "\nExecution error: " + runErr.Error()
	}
	if r.stalled(action, runOutput) {
		return r.res, true
	}
	detail += r.progressNotes() + r.batchingNote(action)
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundProgress, commands: proposedCommands(action), detail: detail,
	})
	r.trail = append(r.trail, fmt.Sprintf("round %d: %s", r.round, truncate(collapse(proposedCommands(action)), 200)))
	r.lastDoing = doingOfRound(action)
	if r.depth == 0 {
		a.trail(r.t, r.trail)
	}
	a.report("round %d done; the model reports more to do", r.round)
	a.log.Info(r.prefix+"continuing: the model reports work left",
		"round", r.round, "max_steps", r.maxSteps,
		"reasoning", truncate(action.Reasoning, 200))
	return TaskResult{}, false
}

// stalled counts a progress round that repeats the one before it, and ends the run when it has
// repeated too many times in a row.
func (r *taskRun) stalled(action Action, runOutput string) bool {
	signature := proposedCommands(action) + "\x00" + runOutput
	// A round that only asked for things it already holds did nothing, whatever
	// its commands were: it counts as a repeat even when the wording differs.
	if signature == r.lastSignature || (r.mem.recalled > 0 && r.mem.executed == 0) {
		r.repeats++
	} else {
		r.repeats = 0
	}
	r.lastSignature = signature
	if r.repeats < stallStopAt {
		return false
	}
	r.res.Reason = fmt.Sprintf("the run stalled: the same actions produced the same output %d rounds in a row "+
		"without the task being reported done. Rephrase the request, or split it into smaller tasks", r.repeats+1)
	r.res.DurationMS = time.Since(r.start).Milliseconds()
	r.a.report("%s", r.res.Reason)
	r.a.log.Warn(r.prefix+"the run stalled", "round", r.round, "repeats", r.repeats+1,
		"commands", proposedCommands(action))
	r.a.escalate(r.ctx, r.prefix)
	return true
}

// progressNotes are the warnings a progress round's record carries for the next round.
func (r *taskRun) progressNotes() string {
	notes := ""
	if r.repeats >= stallWarnAt {
		notes += "\n!! This round repeated the previous one exactly: same actions, same output. " +
			"Repeating it again will not change anything. Do something DIFFERENT, or report " +
			`"done": true if the task is already complete.`
	}
	// Rounds that only READ. Reported from a real session: thirteen rounds of reading and no
	// writing, each one re-reading what the last had already shown. The model is told how
	// long it has been reading, and that what it read is in front of it.
	if cur, ok := treeFingerprint(r.a.cfg.Agent.WorkspaceDir, true); ok && cur != r.lastSources {
		r.lastSources, r.readOnlyRounds = cur, 0
	} else {
		r.readOnlyRounds++
	}
	if r.readOnlyRounds >= readOnlyWarnAt {
		notes += fmt.Sprintf("\n!! %d rounds in a row have only READ; no file has changed. What you read "+
			"is kept in full under \"WHAT YOU HAVE ALREADY READ\". Stop exploring and WRITE the change "+
			"now: create or edit the files this request needs, in this round. If one specific thing is "+
			"still missing, read only that, in the same round as the writing.", r.readOnlyRounds)
	}
	// A check that failed this round is the next round's starting point.
	if failed := r.mem.failedInRound(r.round); len(failed) > 0 {
		notes += failedCheckNote(failed)
	}
	return notes
}

// claimSentBack reports whether a claim of "done" is sent back to the model before the anchor
// judges it, without charging the anchor or max_retries.
func (r *taskRun) claimSentBack(action Action) bool {
	// [7a] A claim of "done" while background agents are still running. Their work would be
	// cancelled and their reports never read, so the claim is sent back ONCE; a second claim
	// means the run does not need them, and they are cancelled.
	if r.a.holdForChildren(r.round, proposedCommands(action), &r.journal) {
		return true
	}

	// [7b] A claim of "done" over a tree that has not changed. Reported from a real session:
	// thirteen rounds of reading, no file written, "done" - and the anchor PASSED, because the
	// project's lint, typecheck and tests are green on code nobody touched. The run closed as
	// complete and the answer said, truthfully, that nothing had been changed.
	//
	// It is told ONCE, and it is not a rejection: nothing is charged to max_retries and the
	// anchor is not spent. A request that only needed an answer (an investigation, a question
	// about the code) is not blocked - the model says so and claims done again, and that claim
	// goes through. What it can no longer do is finish a change it never made without being
	// asked whether that is what it meant.
	if r.unbackedClaim() {
		r.challengeUnchanged(action)
		return true
	}

	// [7c] A claim of "done" while a check the run ran ITSELF is still failing. Reported from a
	// real session: the server's tests failed 22 of 45, the run called them pre-existing without
	// comparing, never tested the endpoint it had written, and claimed done - with the anchor
	// green, because the anchor only runs what the project declares. It is sent back, without
	// charging the anchor or max_retries, up to maxVerifyChallenges times: a run that truly cannot
	// fix a check (a service that is not here) still ends, with the gap in its report.
	if open := r.mem.openFailures(); len(open) > 0 && r.verifyChallenges < maxVerifyChallenges {
		r.challengeFailing(action, open)
		return true
	}

	// [7d] A claim of "done" with a success criterion the run never showed. See audit.go.
	return r.auditClaim(action)
}

// unbackedClaim reports a claim of "done" over a tree that is still the one the run found, after
// rounds that only read, and not yet questioned.
func (r *taskRun) unbackedClaim() bool {
	cur, ok := treeFingerprint(r.a.cfg.Agent.WorkspaceDir, true)
	if !ok || !r.baselineOK || cur != r.baseline {
		return false
	}
	return r.unbackedDone == 0 && r.readOnlyRounds >= doneGuardAfter && !r.a.research
}

// challengeUnchanged sends back a claim of "done" over an unchanged tree.
func (r *taskRun) challengeUnchanged(action Action) {
	r.unbackedDone++
	r.a.report("done claimed but nothing has been changed yet; asking the model to confirm")
	r.a.log.Warn(r.prefix+"done claimed over an unchanged working tree", "round", r.round)
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundProgress, commands: proposedCommands(action),
		detail: "You reported \"done\": true, but NO FILE in the working directory has changed since " +
			"the task began. If the request asked you to add, change, fix or remove something, " +
			"then nothing has been done yet: you already hold what you read under \"WHAT YOU HAVE " +
			"ALREADY READ\", so WRITE the change in this round (create or edit the files), then " +
			"run the project's checks and a test for what you added. If the request only needed " +
			"an answer or an investigation and no file should change, report \"done\": true again " +
			"and say in \"notes\" that nothing had to change and why.",
	})
}

// challengeFailing sends back a claim of "done" while a check the run ran is still failing.
func (r *taskRun) challengeFailing(action Action, open []verifRecord) {
	r.verifyChallenges++
	r.a.report("done claimed while a check is still failing; sending the run back to it (%d/%d)",
		r.verifyChallenges, maxVerifyChallenges)
	r.a.log.Warn(r.prefix+"done claimed over a failing check", "round", r.round,
		"check", truncate(open[0].command, 160), "challenge", r.verifyChallenges)
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundProgress, commands: proposedCommands(action),
		detail: unresolvedCheckChallenge(open, r.verifyChallenges),
	})
}

// finish runs the final action of a claim the anchor passed, and closes the run with the answer.
func (r *taskRun) finish(action Action, validation anchor.Result) (res TaskResult, done bool) {
	a := r.a
	a.report("validation passed; running final action...")
	finalAction, finalErr := a.runFinalAction(r.ctx, action.Final, r.prefix)
	r.res.FinalAction = finalAction
	if finalErr != nil {
		return r.finalActionFailed(action, finalAction, finalErr)
	}
	r.res.Pass = true
	r.res.Reason = validation.Reason
	if blocked := r.mem.blockedChecks(); len(blocked) > 0 {
		r.res.Reason += "; NOT VALIDATED: `" + collapse(truncate(blocked[0].command, 80)) + "` could not run (" +
			blockedCause(blocked[0]) + ")"
	}
	r.res.DurationMS = time.Since(r.start).Milliseconds()
	a.report("task complete: %s", validation.Reason)
	if a.cfg.Prompts.Synthesize.User != "" && a.cfg.Prompts.Synthesize.System != "" {
		r.synthesize(validation)
	}
	// When there was nobody to ask, the run proceeded on a reading the agent chose. Saying
	// so is what makes the result honest: the answer is correct GIVEN that reading, and a
	// user reading it later needs to know which one was taken — otherwise a reasonable
	// assumption looks like a wrong answer.
	if r.proceededOnAssumption != "" {
		r.res.Assumption = r.proceededOnAssumption
		a.report("(proceeded assuming: %s)", r.proceededOnAssumption)
	}
	a.log.Info(r.prefix+"validation passed", "round", r.round, "final_action", finalAction,
		"assumed", truncate(r.proceededOnAssumption, 160))
	return r.res, true
}

// synthesize writes the final answer from the work log.
func (r *taskRun) synthesize(validation anchor.Result) {
	r.a.report("synthesizing answer...")
	// The model's last notes go after the work log: they carry the "Findings:" it noticed and
	// left alone, which is what the synthesis turns into suggestions for the user. They go at
	// the END because the log is cut in its middle, never at its tail.
	notes := ""
	if strings.TrimSpace(r.mem.notes) != "" {
		notes = "## Notes the agent kept while working\n" + r.mem.notes + "\n"
	}
	blocked := r.mem.blockedChecks()
	if rep := r.a.synthesizePhase(r.ctx, r.t, r.workLog.text(notes+unvalidatedBlock(blocked)), validation); rep != nil {
		rep.markUnvalidated(blocked)
		// The summary is the answer, delivered with the result. It is not reported as progress:
		// an interface filed it as a step of the turn, the answer drawn twice.
		r.res.Summary, r.res.Report = rep.Summary, rep
	}
}

// finalActionFailed records a final action that failed after the anchor passed; it is charged
// to max_retries like a rejected claim.
func (r *taskRun) finalActionFailed(action Action, finalAction string, finalErr error) (res TaskResult, done bool) {
	a := r.a
	detail := fmt.Sprintf("validation passed but the final action failed: %v\nOutput: %s", finalErr, finalAction)
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundRejected, commands: proposedCommands(action), detail: detail,
	})
	r.rejected++
	a.report("final action failed: %v", finalErr)
	a.log.Error(r.prefix+"final action failed", "round", r.round, "final_action", finalAction, "error", finalErr)
	if r.rejected > a.cfg.Agent.MaxRetries {
		r.res.Reason = detail
		r.res.DurationMS = time.Since(r.start).Milliseconds()
		a.escalate(r.ctx, r.prefix)
		return r.res, true
	}
	return TaskResult{}, false
}

// rejectClaim records a claim of "done" the anchor refused. THAT is a correction, and
// max_retries is what bounds it — separately from the rounds counter, because the two answer
// different questions.
//
// A gate that failed because a TOOL IS NOT INSTALLED (no node_modules, no venv) says
// nothing about the code, so it is not charged: the run is told to install the project's
// dependencies and try again. Bounded, so a machine that can never install them still ends.
func (r *taskRun) rejectClaim(
	action Action, runOutput string, runErr error, validation anchor.Result,
) (res TaskResult, done bool) {
	a := r.a
	tooling := missingTooling(validation) && r.toolingRetries < maxToolingRetries
	if tooling {
		r.toolingRetries++
	} else {
		r.rejected++
	}
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundRejected, commands: proposedCommands(action),
		detail: a.summariseFailure(action, runOutput, validation, runErr),
	})
	a.report(attemptFailedPrefix+"%s", validation.Reason)
	a.log.Warn(r.prefix+"attempt failed",
		"attempt", r.rejected, "max_attempts", a.cfg.Agent.MaxRetries+1,
		"round", r.round, "max_steps", r.maxSteps,
		"commands", proposedCommands(action),
		"validation", validation.Reason)

	if !tooling && r.rejected > a.cfg.Agent.MaxRetries {
		// The retries ran out. This is a DIFFERENT outcome from the step budget
		// running out, and the message has to say which one it was, or the reader
		// goes looking for a broken check.
		r.res.Reason = fmt.Sprintf("all %d attempts were exhausted without passing validation: %s",
			a.cfg.Agent.MaxRetries+1, validation.Reason)
		r.res.DurationMS = time.Since(r.start).Milliseconds()
		a.report("%s", r.res.Reason)
		a.escalate(r.ctx, r.prefix)
		return r.res, true
	}
	return TaskResult{}, false
}
