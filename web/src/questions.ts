// The questions the agent asks when it cannot read a request well enough, and how they are
// answered. Kept apart from the card (QuestionsCard.tsx) and free of imports so the tests can load
// it as it is. It follows the terminal's window (internal/tui/ask.go): every question on screen at
// once, answered in any order, and confirmed together as one turn.

// AskItem is one question, as the gateway sends it (agent.AskItem).
export interface AskItem {
  text: string;
  // assumption is what the agent does when the question is left unanswered.
  assumption?: string;
  // options are the answers it offers; empty when the question has no short list of them.
  options?: string[];
}

// QuestionAnswers is how a turn's questions were answered, as the gateway keeps it with the turn:
// one answer per question ("" for one left to the assumption) and the turn that sent them.
export interface QuestionAnswers {
  answers: string[];
  request: string;
}

// ANSWERS_LEAD opens the answers in the turn that carries them. It is the terminal's own line
// (composeAnswers), so both clients send the agent the same shape.
export const ANSWERS_LEAD = "Answers to what you asked:";

// answerRequest is the turn that carries the answers: the request being clarified, then each
// question with its answer. A question left blank is sent with the assumption the agent offered,
// so the agent reads every gap as settled; one with neither is left out. With nothing to send the
// request goes back as it was.
export function answerRequest(
  origin: string,
  items: AskItem[],
  answers: string[],
): string {
  const lines: string[] = [];
  items.forEach((it, i) => {
    const a = (answers[i] ?? "").trim() || (it.assumption ?? "").trim();
    if (a) {
      lines.push(`- ${it.text} -> ${a}`);
    }
  });
  const head = origin.trim();
  if (lines.length === 0) {
    return head;
  }
  return [head, "", ANSWERS_LEAD, ...lines].join("\n").trim();
}

// answersLabel is what the turn carrying the answers is drawn as: the answers themselves, in order,
// with the assumption standing in for one left blank. It reads as what the user said, not as the
// prompt the agent is sent.
export function answersLabel(items: AskItem[], answers: string[]): string {
  return items
    .map((it, i) => (answers[i] ?? "").trim() || (it.assumption ?? "").trim())
    .filter((a) => a !== "")
    .join(" · ");
}

// isAnswering says whether a user turn is the one sent with the answers `qa`: it is then drawn as
// the answers, not as the prompt it carries.
export function isAnswering(
  qa: QuestionAnswers | undefined,
  text: string | undefined,
): boolean {
  return !!qa?.request && qa.request === text;
}

// hasQuestions says the gateway sent a list of questions with at least one in it.
export function hasQuestions(q: unknown): q is AskItem[] {
  return Array.isArray(q) && q.length > 0;
}
