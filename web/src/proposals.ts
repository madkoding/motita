// The suggestions a finished task offers, and the rule that answers them. Kept apart from the card
// (Report.tsx) and free of imports so the tests can load it as it is.
//
// Each suggestion is answered with one of three: apply this one, apply this one and the following,
// or apply none. It is the same rule the terminal's window follows.

export type Verdict = "apply" | "skip";
export type ProposalAnswer = "this" | "rest" | "none";

// settle answers suggestion i and, for "rest" or "none", every undecided one after it, so a list is
// one click to accept or dismiss. The ones before i keep their own answers, and so does one after
// it that was already decided (a needed suggestion auto mode accepted on its own).
export function settle(
  verdicts: (Verdict | undefined)[],
  i: number,
  answer: ProposalAnswer,
): (Verdict | undefined)[] {
  const next = [...verdicts];
  let fill: Verdict = "apply";
  if (answer === "none") {
    fill = "skip";
  }
  next[i] = fill;
  if (answer !== "this") {
    for (let k = i + 1; k < next.length; k++) {
      next[k] = next[k] ?? fill;
    }
  }
  return next;
}

// initialVerdicts is where a list of suggestions starts: as it was answered, or, in a turn auto
// mode routed, with the needed ones already accepted. Those are the ones whose answer is obvious
// from the context, so auto mode does not ask about them.
export function initialVerdicts(
  suggestions: { needed?: boolean }[],
  auto: boolean,
  answered?: Verdict[],
): (Verdict | undefined)[] {
  if (answered) {
    return answered;
  }
  return suggestions.map((s) => {
    if (auto && s.needed) {
      return "apply";
    }
    return undefined;
  });
}

// proposalRequest is the message that applies the accepted suggestions and nothing else, behind
// `lead` (in the interface's language), or "" when none was accepted. Title and detail both go,
// because the turn that proposed them is over and the next one has to know exactly what was meant.
export function proposalRequest(
  lead: string,
  suggestions: { title?: string; detail?: string }[],
  verdicts: (Verdict | undefined)[],
): string {
  const lines = suggestions
    .filter((_, i) => verdicts[i] === "apply")
    .map(proposalLine);
  if (lines.length === 0) {
    return "";
  }
  return [lead.trim(), ...lines].join("\n");
}

// proposalLine is one accepted suggestion as a line of the request: its title, then its detail.
function proposalLine(s: { title?: string; detail?: string }): string {
  let line = `- ${s.title ?? ""}`;
  if (s.detail) {
    line += `: ${s.detail}`;
  }
  return line;
}

// Pick is the button pressed on one suggestion, or "" on one settled by an earlier answer.
export type Pick = ProposalAnswer | "";

// SuggestionAnswers is how a report's suggestions were answered, as the gateway keeps it with
// the turn: the button pressed on each, what was decided for each, and the turn sent to apply the
// accepted ones ("" or absent when none was).
export interface SuggestionAnswers {
  picks: Pick[];
  verdicts: Verdict[];
  request?: string;
}

// pickAt records the button pressed on suggestion i. The ones it settles keep "" as their pick:
// only the button the user actually pressed is drawn as pressed.
export function pickAt(
  picks: Pick[],
  i: number,
  answer: ProposalAnswer,
): Pick[] {
  const next = [...picks];
  next[i] = answer;
  return next;
}

// isApplying says whether a user turn is the one sent to apply the suggestions answered with
// `answers`: it is then drawn as a short label, not as the prompt it carries.
export function isApplying(
  answers: SuggestionAnswers | undefined,
  text: string | undefined,
): boolean {
  return !!answers?.request && answers.request === text;
}
