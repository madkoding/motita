import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "preact/hooks";
import { Markdown } from "./Markdown";
import { Lightbox } from "./Lightbox";
import { t, tf, plural } from "./i18n";
import { choose } from "./choose";
import {
  pickAt,
  proposalRequest,
  initialVerdicts,
  settle,
  type Pick,
  type ProposalAnswer,
  type SuggestionAnswers,
  type Verdict,
} from "./proposals";

// The structured account of a finished task, as the gateway sends it in the `done` event
// (agent.Report in Go). Every field is optional here on purpose: a report from a build that
// predates a field must still draw, and the component owes nothing to a field that is absent.
export interface TaskReport {
  version?: number;
  status?: string;
  summary?: string;
  changes?: { path?: string; kind?: string; description?: string }[];
  verification?: { check?: string; result?: string; evidence?: string }[];
  evidence?: {
    title?: string;
    before?: string;
    after?: string;
    caption?: string;
  }[];
  risks?: string[];
  next_steps?: string[];
  // What the agent noticed and did NOT do. Each is a proposal the user decides on; nothing in it
  // has been applied.
  // `needed` marks the ones whose answer is obvious: auto mode applies them without asking.
  // `why_needed` says what makes one needed, so the decision taken for the user can be checked.
  suggestions?: {
    title?: string;
    detail?: string;
    needed?: boolean;
    why_needed?: string;
  }[];
}

const STATUS_TEXT: Record<string, string> = {
  done: "Done",
  partial: "Partly done",
  failed: "Not completed",
};
const KIND_TEXT: Record<string, string> = {
  added: "new",
  modified: "edited",
  deleted: "removed",
  other: "changed",
};
const RESULT_MARK: Record<string, string> = {
  pass: "✓",
  fail: "✕",
  skipped: "–",
};

// hasReport says whether there is anything beyond the summary worth a card. A report that is only
// a sentence is drawn as the plain message it always was.
export function hasReport(r?: TaskReport | null): r is TaskReport {
  if (!r) {
    return false;
  }
  const counts = [
    r.changes?.length,
    r.verification?.length,
    r.evidence?.length,
    r.risks?.length,
    r.next_steps?.length,
    r.suggestions?.length,
  ];
  return counts.some((n) => !!n);
}

// ReportCard lays the report out: the verdict first, then the answer, then the proof. The proof
// is the point of it — what changed and what was checked are separate lists, so a reader sees at
// a glance whether the work was verified and by what.
// Evidence images are artifacts of the session, behind the token, so a plain <img src> cannot load
// them: `loadImage` fetches one and hands back an object URL (or null when it cannot be read).
export type LoadImage = (name: string) => Promise<string | null>;

function useImage(name: string | undefined, load?: LoadImage) {
  const [src, setSrc] = useState<string | null | undefined>(undefined);
  useEffect(() => {
    let live = true;
    setSrc(undefined);
    if (!name || !load) {
      setSrc(null);
      return undefined;
    }
    load(name)
      .then((u) => {
        if (live) {
          setSrc(u);
        }
      })
      .catch(() => {
        if (live) {
          setSrc(null);
        }
      });
    return () => {
      live = false;
    };
  }, [name, load]);
  return src;
}

function Shot({
  kind,
  name,
  label,
  load,
  onOpen,
}: {
  kind: "before" | "after";
  name?: string;
  label: string;
  load?: LoadImage;
  onOpen: (src: string, label: string) => void;
}) {
  const src = useImage(name, load);
  const cap = choose(kind === "before", t("Before"), t("After"));
  return (
    <figure class={`rpt-shot ${kind}`}>
      <figcaption>{cap}</figcaption>
      {!!src && (
        <button
          type="button"
          class="rpt-shot-btn"
          title={t("Click to enlarge")}
          onClick={() => onOpen(src, `${label} — ${cap}`)}
        >
          <img src={src} alt={`${label} — ${cap}`} loading="lazy" />
        </button>
      )}
      {!src && (
        <div class="rpt-shot-empty">
          {choose(
            !name,
            t("No image"),
            choose(src === undefined, t("Loading…"), t("Image not available")),
          )}
        </div>
      )}
    </figure>
  );
}

type Change = NonNullable<TaskReport["changes"]>[number];
type Check = NonNullable<TaskReport["verification"]>[number];
type Evidence = NonNullable<TaskReport["evidence"]>[number];
type Zoom = { images: { src: string; label: string }[]; start: number };

// keyed gives each item of a list a key made of its content and of how many identical items came
// before it, so two equal lines still get keys of their own. It runs once per report, not per draw.
function keyed<T>(
  items: T[] | undefined,
  content: (item: T) => string,
): { key: string; item: T }[] {
  const seen = new Map<string, number>();
  return (items ?? []).map((item) => {
    const c = content(item);
    const n = seen.get(c) ?? 0;
    seen.set(c, n + 1);
    return { key: `${c}#${n}`, item };
  });
}

// keyedReport is the report's lists with their keys.
function keyedReport(report: TaskReport) {
  return {
    changes: keyed(
      report.changes,
      (c) => `${c.kind}|${c.path}|${c.description}`,
    ),
    checks: keyed(
      report.verification,
      (c) => `${c.result}|${c.check}|${c.evidence}`,
    ),
    evidence: keyed(
      report.evidence,
      (e) => `${e.title}|${e.before}|${e.after}|${e.caption}`,
    ),
    risks: keyed(report.risks, (r) => r),
    next: keyed(report.next_steps, (n) => n),
  };
}

export function ReportCard({
  report,
  loadImage,
  answers,
  expired,
  onAnswer,
  auto,
}: {
  report: TaskReport;
  // auto says the turn went through auto mode, which applies the needed suggestions on its own.
  auto?: boolean;
  loadImage?: LoadImage;
  // answers are how the suggestions were already answered, when they were.
  answers?: SuggestionAnswers;
  // expired says the conversation moved on without answering them: they are no longer offered.
  expired?: boolean;
  // onAnswer keeps the answers and starts the turn that applies the accepted suggestions. Without
  // it (a run in flight) the suggestions are shown but cannot be answered.
  onAnswer?: (answers: SuggestionAnswers) => void;
}) {
  const [zoom, setZoom] = useState<Zoom | null>(null);
  const closeZoom = useCallback(() => setZoom(null), []);
  // The object URLs of the images loaded so far, so the preview can offer the other half of a pair.
  const urls = useRef(new Map<string, string>());
  const load = useCallback<LoadImage>(
    async (name) => {
      if (!loadImage) {
        return null;
      }
      const u = await loadImage(name);
      if (u) {
        urls.current.set(name, u);
      }
      return u;
    },
    [loadImage],
  );
  const status = choose(
    STATUS_TEXT[report.status ?? ""],
    report.status as string,
    "done",
  );
  const { changes, checks, evidence, risks, next } = useMemo(
    () => keyedReport(report),
    [report],
  );
  return (
    <div class={`rpt rpt-${status}`}>
      <ReportHead
        status={status}
        checks={report.verification ?? []}
        changes={changes.length}
      />
      {report.summary && (
        <div class="rpt-summary">
          <Markdown content={report.summary} />
        </div>
      )}

      {changes.length > 0 && <ChangesSection changes={changes} />}
      {checks.length > 0 && <ChecksSection checks={checks} />}

      {evidence.length > 0 && (
        <section class="rpt-sec">
          <h4>{t("Before and after")}</h4>
          {evidence.map(({ key, item }) => (
            <EvidenceSet
              key={key}
              e={item}
              load={load}
              urls={urls}
              onZoom={setZoom}
            />
          ))}
        </section>
      )}

      {risks.length > 0 && (
        <BulletSection
          cls="rpt-risks"
          title={t("Worth knowing")}
          items={risks}
        />
      )}
      {next.length > 0 && (
        <BulletSection cls="rpt-next" title={t("Next")} items={next} />
      )}
      {(report.suggestions?.length ?? 0) > 0 && (
        <SuggestionsSection
          suggestions={report.suggestions ?? []}
          answers={answers}
          expired={expired}
          onAnswer={onAnswer}
          auto={!!auto}
        />
      )}
      {zoom && (
        <Lightbox images={zoom.images} start={zoom.start} onClose={closeZoom} />
      )}
    </div>
  );
}

// ReportHead is the verdict and the tallies.
function ReportHead({
  status,
  checks,
  changes,
}: {
  status: string;
  checks: Check[];
  changes: number;
}) {
  const passed = checks.filter((c) => c.result === "pass").length;
  const failed = checks.filter((c) => c.result === "fail").length;
  return (
    <div class="rpt-head">
      <span class="rpt-badge">{t(STATUS_TEXT[status])}</span>
      {checks.length > 0 && (
        <span
          class={`rpt-tally${choose(failed, " bad", "")}`}
          title={t("checks that passed")}
        >
          {tf("{passed}/{total} checks passed", {
            passed,
            total: checks.length,
          })}
        </span>
      )}
      {changes > 0 && (
        <span class="rpt-tally">
          {plural(changes, "1 change", "{n} changes")}
        </span>
      )}
    </div>
  );
}

function ChangesSection({
  changes,
}: {
  changes: { key: string; item: Change }[];
}) {
  return (
    <section class="rpt-sec">
      <h4>{t("What changed")}</h4>
      <ul class="rpt-changes">
        {changes.map(({ key, item: c }) => (
          <li key={key}>
            <span class={`rpt-kind ${c.kind ?? "other"}`}>
              {t(KIND_TEXT[c.kind ?? "other"] ?? "changed")}
            </span>
            <span class="rpt-body">
              {c.path && <code class="rpt-path">{c.path}</code>}
              {c.description && <span class="rpt-desc">{c.description}</span>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function ChecksSection({ checks }: { checks: { key: string; item: Check }[] }) {
  return (
    <section class="rpt-sec">
      <h4>{t("How it was checked")}</h4>
      <ul class="rpt-checks">
        {checks.map(({ key, item: c }) => (
          <li key={key} class={c.result ?? "skipped"}>
            <span class="rpt-mark" aria-label={t(c.result ?? "skipped")}>
              {RESULT_MARK[c.result ?? "skipped"] ?? "–"}
            </span>
            <span class="rpt-body">
              <span class="rpt-check">{c.check}</span>
              {c.evidence && <span class="rpt-evidence">{c.evidence}</span>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

// EvidenceSet is one before/after pair with its title and caption.
function EvidenceSet({
  e,
  load,
  urls,
  onZoom,
}: {
  e: Evidence;
  load: LoadImage;
  urls: { current: Map<string, string> };
  onZoom: (z: Zoom) => void;
}) {
  const label = e.title || t("Screenshot");
  // The preview walks the pair: the other half is one arrow away. A half that has not
  // loaded yet is simply not in the set.
  const open = (kind: "before" | "after") => (src: string, lbl: string) => {
    const other = choose(kind === "before", e.after, e.before);
    let otherSrc: string | undefined;
    if (other) {
      otherSrc = urls.current.get(other);
    }
    const mine = { src, label: lbl };
    let pair = [mine];
    let start = 0;
    if (otherSrc && kind === "before") {
      pair = [mine, { src: otherSrc, label: `${label} — ${t("After")}` }];
    } else if (otherSrc) {
      pair = [{ src: otherSrc, label: `${label} — ${t("Before")}` }, mine];
      start = 1;
    } else {
      // Only this half has loaded: the preview shows it alone.
    }
    onZoom({ images: pair, start });
  };
  return (
    <div class="rpt-evidence-set">
      {e.title && <div class="rpt-evidence-title">{e.title}</div>}
      <div class="rpt-shots">
        {e.before !== undefined && e.before !== "" && (
          <Shot
            kind="before"
            name={e.before}
            label={label}
            load={load}
            onOpen={open("before")}
          />
        )}
        {e.after !== undefined && e.after !== "" && (
          <Shot
            kind="after"
            name={e.after}
            label={label}
            load={load}
            onOpen={open("after")}
          />
        )}
      </div>
      {e.caption && <div class="rpt-evidence-caption">{e.caption}</div>}
    </div>
  );
}

// BulletSection is a titled list of plain lines: the risks, the next steps.
function BulletSection({
  cls,
  title,
  items,
}: {
  cls: string;
  title: string;
  items: { key: string; item: string }[];
}) {
  return (
    <section class={`rpt-sec ${cls}`}>
      <h4>{title}</h4>
      <ul>
        {items.map(({ key, item }) => (
          <li key={key}>{item}</li>
        ))}
      </ul>
    </section>
  );
}

type Suggestion = NonNullable<TaskReport["suggestions"]>[number];

// The three buttons of a suggestion, in the order they are drawn.
const ANSWERS: { pick: ProposalAnswer; label: () => string }[] = [
  { pick: "this", label: () => t("Apply this one") },
  { pick: "rest", label: () => t("Apply this one and the following") },
  { pick: "none", label: () => t("Apply none") },
];

// SuggestionsSection asks about each suggestion in turn: apply this one, apply this one and the
// following, or apply none. Nothing is applied until every one has an answer, and then only the
// accepted ones are sent, as a new turn. The button pressed stays on screen, marked and no longer
// pressable, so the card says what was chosen; `answers` brings that back when the conversation
// is opened again.
function SuggestionsSection({
  suggestions,
  answers,
  expired,
  onAnswer,
  auto,
}: {
  suggestions: Suggestion[];
  answers?: SuggestionAnswers;
  expired?: boolean;
  onAnswer?: (answers: SuggestionAnswers) => void;
  auto: boolean;
}) {
  const [verdicts, setVerdicts] = useState<(Verdict | undefined)[]>(() =>
    initialVerdicts(suggestions, auto, answers?.verdicts),
  );
  const [picks, setPicks] = useState<Pick[]>(
    () => answers?.picks ?? suggestions.map(() => ""),
  );
  // Expired only while nothing was answered here: a half-answered list keeps its marks.
  const stale = !!expired && picks.every((p) => !p);
  const done = stale || verdicts.every((v) => v !== undefined);
  const current = verdicts.findIndex((v) => v === undefined);
  const finish = (next: (Verdict | undefined)[], pressed: Pick[]) => {
    if (!onAnswer) {
      return;
    }
    const request = proposalRequest(
      t("Apply these suggestions you made, and nothing else:"),
      suggestions,
      next,
    );
    onAnswer({ picks: pressed, verdicts: next as Verdict[], request });
  };
  const answer = (pick: ProposalAnswer) => {
    const next = settle(verdicts, current, pick);
    const pressed = pickAt(picks, current, pick);
    setVerdicts(next);
    setPicks(pressed);
    if (next.every((v) => v !== undefined)) {
      finish(next, pressed);
    }
  };
  // In auto mode a list whose every suggestion is needed has nothing to ask: it is applied as
  // soon as it can be (no run in flight), once.
  const applied = useRef(false);
  useEffect(() => {
    if (applied.current || answers || stale || !onAnswer || !auto) {
      return;
    }
    if (verdicts.every((v) => v === "apply")) {
      applied.current = true;
      finish(verdicts, picks);
    }
  });
  const items = keyed(suggestions, (s) => `${s.title}|${s.detail}`);
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
      <ul>
        {items.map(({ key, item }, i) => (
          <li
            key={key}
            class={
              verdicts[i] ?? choose(i === current && !stale, "current", "")
            }
          >
            <span class="rpt-body">
              <span class="rpt-check">{item.title}</span>
              {item.detail && <span class="rpt-evidence">{item.detail}</span>}
            </span>
            {auto && item.needed && (
              <span class="rpt-needed" title={item.why_needed}>
                {t("needed")}
                {item.why_needed && `: ${item.why_needed}`}
              </span>
            )}
            {verdicts[i] && (
              <span class="rpt-verdict">
                {choose(
                  verdicts[i] === "apply",
                  t("will apply"),
                  t("not applied"),
                )}
              </span>
            )}
            {(picks[i] || (i === current && !done)) && (
              <div class="rpt-suggest-actions">
                {ANSWERS.map(({ pick, label }) => (
                  <button
                    key={pick}
                    type="button"
                    class={choose(picks[i] === pick, "picked", undefined)}
                    aria-pressed={!!picks[i] && picks[i] === pick}
                    disabled={!!picks[i] || !onAnswer}
                    onClick={() => answer(pick)}
                  >
                    {choose(picks[i] === pick, "✓ ", "")}
                    {label()}
                  </button>
                ))}
              </div>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
