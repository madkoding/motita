// The suggestions a finished task offers, and the rule that answers them. Kept apart from the card
// (Report.tsx) and free of imports so the tests can load it as it is.
//
// The web card lists them with a box each: the ticked ones are applied with Apply, and Ignore
// applies none. The terminal's window asks about them one at a time (apply this one, this one and
// the following, none), and the gateway keeps the answers in that shape for both.

export type Verdict = "apply" | "skip";
export type ProposalAnswer = "this" | "rest" | "none";

// initialVerdicts is where a list of suggestions starts: as it was answered, or, in a turn auto
// mode routed, with the needed ones already accepted. Those are the ones whose answer is obvious
// from the context, so auto mode does not ask about them. With `all` (the setting that applies
// every suggestion without asking) every one starts accepted.
export function initialVerdicts(
  suggestions: { needed?: boolean }[],
  auto: boolean,
  answered?: Verdict[],
  all = false,
): (Verdict | undefined)[] {
  if (answered) {
    return answered;
  }
  return suggestions.map((s) => {
    if (all) {
      return "apply";
    }
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

// answersOf is how a list was answered when the user ticked some suggestions and pressed Apply (or
// none and pressed Ignore): the ticked ones are "this", the rest "none", so every suggestion has
// a button recorded, the way the gateway keeps them. The request is "" when none was ticked.
function verdictOf(on: boolean): Verdict {
  if (on) {
    return "apply";
  }
  return "skip";
}

function pickOf(on: boolean): Pick {
  if (on) {
    return "this";
  }
  return "none";
}

export function answersOf(
  lead: string,
  suggestions: { title?: string; detail?: string }[],
  ticked: boolean[],
): SuggestionAnswers {
  const verdicts = ticked.map(verdictOf);
  const picks = ticked.map(pickOf);
  return {
    picks,
    verdicts,
    request: proposalRequest(lead, suggestions, verdicts),
  };
}

// isApplying says whether a user turn is the one sent to apply the suggestions answered with
// `answers`: it is then drawn as a short label, not as the prompt it carries.
export function isApplying(
  answers: SuggestionAnswers | undefined,
  text: string | undefined,
): boolean {
  return !!answers?.request && answers.request === text;
}
