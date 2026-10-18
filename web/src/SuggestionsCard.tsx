// The suggestions a finished task offers, as a card to answer: every suggestion a row with a box
// to tick, laid out like the questions card (QuestionsCard.tsx), and at the end one button that
// applies the ticked ones and one that ignores them all. The answers are kept the way the gateway
// keeps them (proposals.ts), so a conversation opened again draws the same ticks.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { choose } from "./choose";
import { plural, t, tf } from "./i18n";
import {
  answersOf,
  initialVerdicts,
  type SuggestionAnswers,
} from "./proposals";

export interface SuggestionItem {
  title?: string;
  detail?: string;
  needed?: boolean;
  why_needed?: string;
}

// keyOf is a suggestion's key: its content, and how many identical ones came before it.
function keysOf(items: SuggestionItem[]): string[] {
  const seen = new Map<string, number>();
  return items.map((s) => {
    const base = `${s.title}|${s.detail}`;
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    return `${base}#${n}`;
  });
}

// tickedAt is where the card starts: as it was answered, or with the ones the mode accepts already
// ticked (the needed ones in auto mode, all of them when the settings apply everything).
function tickedAt(
  suggestions: SuggestionItem[],
  auto: boolean,
  all: boolean,
  answered?: SuggestionAnswers,
): boolean[] {
  return initialVerdicts(suggestions, auto, answered?.verdicts, all).map(
    (v) => v === "apply",
  );
}

// SuggestionsSection offers the suggestions of a report: tick the ones to apply and press Apply,
// or press Ignore to apply none. Nothing is applied before one of the two is pressed, except when
// the mode or the settings apply them without asking: then the card ticks them all and presses
// Apply on its own, once, as soon as it can (no run in flight).
export function SuggestionsSection({
  suggestions,
  answers,
  expired,
  onAnswer,
  auto,
  all,
}: {
  suggestions: SuggestionItem[];
  answers?: SuggestionAnswers;
  expired?: boolean;
  onAnswer?: (answers: SuggestionAnswers) => void;
  // auto says the turn went through auto mode, which applies the needed suggestions on its own.
  auto: boolean;
  // all says the settings apply every suggestion without asking.
  all: boolean;
}) {
  const [ticked, setTicked] = useState<boolean[]>(() =>
    tickedAt(suggestions, auto, all, answers),
  );
  const [sent, setSent] = useState<SuggestionAnswers | undefined>(answers);
  // Expired only while nothing was answered here: an answered list keeps its marks.
  const stale = !!expired && !sent;
  const locked = !!sent || stale || !onAnswer;
  const lead = t("Apply these suggestions you made, and nothing else:");
  const submit = (on: boolean[]) => {
    const a = answersOf(lead, suggestions, on);
    setSent(a);
    onAnswer?.(a);
  };
  const toggle = (i: number) => {
    setTicked(ticked.map((on, k) => on !== (k === i)));
  };
  const apply = useCallback(() => submit(ticked), [ticked, onAnswer]);
  const ignore = useCallback(
    () => submit(suggestions.map(() => false)),
    [suggestions, onAnswer],
  );
  const applied = useRef(false);
  const autoApplies = !!onAnswer && (auto || all) && !sent && !stale;
  useEffect(() => {
    if (!applied.current && autoApplies && ticked.every(Boolean)) {
      applied.current = true;
      submit(ticked);
    }
  });
  const count = ticked.filter(Boolean).length;
  const keys = keysOf(suggestions);
  return (
    <section class={`rpt-sec rpt-suggest${choose(stale, " expired", "")}`}>
      <h4>{t("Suggestions")}</h4>
      {stale && (
        <p class="rpt-expired">
          {t(
            "Not answered before the conversation moved on; no longer offered.",
          )}
        </p>
      )}
      <div class="sg-list">
        {suggestions.map((s, i) => (
          <Row
            key={keys[i]}
            item={s}
            on={!!ticked[i]}
            locked={locked}
            auto={auto}
            index={i}
            onToggle={toggle}
          />
        ))}
      </div>
      <Footer
        sent={sent}
        stale={stale}
        locked={locked}
        count={count}
        total={suggestions.length}
        onApply={apply}
        onIgnore={ignore}
      />
    </section>
  );
}

// Row is one suggestion: its box, its title and its detail, and why it is needed when auto mode
// ticked it on its own.
function Row({
  item,
  on,
  locked,
  auto,
  index,
  onToggle,
}: {
  item: SuggestionItem;
  on: boolean;
  locked: boolean;
  auto: boolean;
  index: number;
  // onToggle ticks or unticks the suggestion at `index`.
  onToggle: (i: number) => void;
}) {
  const change = useCallback(() => onToggle(index), [onToggle, index]);
  return (
    <label class={`qcard-opt sg-row${choose(on, " picked", "")}`}>
      <input
        type="checkbox"
        class="sg-box"
        checked={on}
        disabled={locked}
        onChange={change}
      />
      <span class="qcard-key" aria-hidden="true">
        {choose(on, "✓", "")}
      </span>
      <span class="qcard-opt-text">
        <span class="sg-title">{item.title}</span>
        {item.detail && <span class="sg-detail">{item.detail}</span>}
        {auto && item.needed && (
          <span class="rpt-needed" title={item.why_needed}>
            {t("needed")}
            {item.why_needed && `: ${item.why_needed}`}
          </span>
        )}
      </span>
    </label>
  );
}

// Footer is the end of the card: Apply with how many are ticked, and Ignore; once answered, what
// was decided.
function Footer({
  sent,
  stale,
  locked,
  count,
  total,
  onApply,
  onIgnore,
}: {
  sent?: SuggestionAnswers;
  stale: boolean;
  locked: boolean;
  count: number;
  total: number;
  onApply: () => void;
  onIgnore: () => void;
}) {
  if (sent) {
    const n = sent.verdicts.filter((v) => v === "apply").length;
    return (
      <div class="qcard-foot">
        <span class={choose(n > 0, "qcard-done", "sg-ignored")}>
          {choose(
            n > 0,
            "✓ " + plural(n, "1 suggestion applied", "{n} suggestions applied"),
            t("Ignored"),
          )}
        </span>
      </div>
    );
  }
  if (stale) {
    return null;
  }
  return (
    <div class="qcard-foot">
      <button
        type="button"
        class="qcard-send"
        disabled={locked || count === 0}
        onClick={onApply}
      >
        {choose(count > 0, tf("Apply ({n})", { n: count }), t("Apply"))}
      </button>
      <button
        type="button"
        class="sg-ignore"
        disabled={locked}
        onClick={onIgnore}
      >
        {t("Ignore")}
      </button>
      <small class="qcard-hint">
        {tf("{done} of {total} selected", { done: count, total })}
      </small>
    </div>
  );
}
