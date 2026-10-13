// The suggestions a finished task offers, and the rule that answers them. Kept apart from the card
// (Report.tsx) and free of imports so the tests can load it as it is.
//
// Each suggestion is answered with one of three: apply this one, apply this one and the following,
// or apply none. It is the same rule the terminal's window follows.

export type Verdict = "apply" | "skip";
export type ProposalAnswer = "this" | "rest" | "none";

// settle answers suggestion i and, for "rest" or "none", every one after it, so a list is one
// click to accept or dismiss. The ones before i keep their own answers.
export function settle(
  verdicts: (Verdict | undefined)[],
  i: number,
  answer: ProposalAnswer,
): (Verdict | undefined)[] {
  const next = [...verdicts];
  const fill: Verdict = answer === "none" ? "skip" : "apply";
  next[i] = fill;
  if (answer !== "this") {
    for (let k = i + 1; k < next.length; k++) {
      next[k] = fill;
    }
  }
  return next;
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
    .map((s) => `- ${s.title ?? ""}${s.detail ? `: ${s.detail}` : ""}`);
  if (lines.length === 0) {
    return "";
  }
  return [lead.trim(), ...lines].join("\n");
}
