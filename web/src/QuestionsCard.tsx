// The questions the agent asked before doing anything, as a card to answer: every question at once,
// each with its options as buttons, a field for an answer of one's own, and what the agent does if
// it is left blank. One button sends them all as the next turn, the way the terminal's window does.
import { useState } from "preact/hooks";
import { choose } from "./choose";
import { plural, t, tf } from "./i18n";
import {
  answerRequest,
  answersLabel,
  type AskItem,
  type QuestionAnswers,
} from "./questions";

export function QuestionsCard({
  items,
  origin,
  answers,
  expired,
  onAnswer,
}: {
  items: AskItem[];
  // origin is the request the questions are about, sent again with the answers.
  origin: string;
  // answers are how the questions were already answered, when they were.
  answers?: QuestionAnswers;
  // expired says the conversation moved on without answering them: they are no longer offered.
  expired?: boolean;
  // onAnswer keeps the answers and sends them as the next turn, under `shown`. Without it (a run
  // in flight) the questions are shown but cannot be answered.
  onAnswer?: (answers: QuestionAnswers, shown: string) => void;
}) {
  const [given, setGiven] = useState<string[]>(
    () => answers?.answers ?? items.map(() => ""),
  );
  const sent = !!answers;
  const stale = !!expired && !sent;
  const locked = sent || stale || !onAnswer;
  const set = (i: number, value: string) => {
    const next = [...given];
    next[i] = value;
    setGiven(next);
  };
  const filled = given.filter((a) => a.trim() !== "").length;
  const send = (e: Event) => {
    e.preventDefault();
    if (locked) {
      return;
    }
    const request = answerRequest(origin, items, given);
    onAnswer?.({ answers: given, request }, answersLabel(items, given));
  };
  return (
    <form
      class={`qcard${choose(stale, " expired", "")}${choose(sent, " sent", "")}`}
      onSubmit={send}
    >
      <div class="qcard-head">
        <strong>
          {plural(
            items.length,
            "Motita needs an answer to continue",
            "Motita needs {n} answers to continue",
          )}
        </strong>
        {items.length > 1 && (
          <span class="qcard-count">
            {tf("{done} of {total} answered", {
              done: filled,
              total: items.length,
            })}
          </span>
        )}
      </div>
      {stale && (
        <p class="qcard-note">
          {t("Not answered before the conversation moved on; no longer offered.")}
        </p>
      )}
      <ol class="qcard-list">
        {items.map((it, i) => (
          <Question
            key={`${i}|${it.text}`}
            n={i + 1}
            item={it}
            value={given[i]}
            locked={locked}
            onChange={(v) => set(i, v)}
          />
        ))}
      </ol>
      <div class="qcard-foot">
        {choose(
          sent,
          <span class="qcard-done">✓ {t("Answers sent")}</span>,
          <button type="submit" class="qcard-send" disabled={locked}>
            {t("Send answers")}
          </button>,
        )}
        {!sent && !stale && (
          <small class="qcard-hint">
            {t("A question left blank goes with what Motita suggests.")}
          </small>
        )}
      </div>
    </form>
  );
}

// Question is one question: its text, its options as buttons, a field for an answer of one's own,
// and what happens when it is left blank. Picking the chosen option again clears it.
function Question({
  n,
  item,
  value,
  locked,
  onChange,
}: {
  n: number;
  item: AskItem;
  value: string;
  locked: boolean;
  onChange: (v: string) => void;
}) {
  const options = item.options ?? [];
  const own = !!value && !options.includes(value);
  const id = `qcard-q${n}`;
  return (
    <li class={choose(value.trim() !== "", "answered", undefined)}>
      <p class="qcard-q" id={id}>
        <span class="qcard-num" aria-hidden="true">
          {n}
        </span>
        <span>{item.text}</span>
      </p>
      {options.length > 0 && (
        <div class="qcard-opts" role="radiogroup" aria-labelledby={id}>
          {options.map((o) => (
            <button
              key={o}
              type="button"
              role="radio"
              aria-checked={value === o}
              class={choose(value === o, "picked", undefined)}
              disabled={locked}
              onClick={() => onChange(choose(value === o, "", o))}
            >
              {choose(value === o, "✓ ", "")}
              {o}
            </button>
          ))}
        </div>
      )}
      {!locked && (
        <input
          type="text"
          class="qcard-own"
          aria-labelledby={id}
          placeholder={choose(
            options.length > 0,
            t("Or write your own answer"),
            t("Write your answer"),
          )}
          value={choose(own, value, "")}
          onInput={(e) => onChange((e.target as HTMLInputElement).value)}
        />
      )}
      {locked && own && <p class="qcard-yours">{value}</p>}
      {item.assumption && !value && (
        <p class="qcard-assume">
          {tf("If you leave it blank: {assumption}", {
            assumption: item.assumption,
          })}
        </p>
      )}
    </li>
  );
}
