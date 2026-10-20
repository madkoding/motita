import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import type { Api } from "./llmApi";
import {
  type EvalCaseResult,
  type EvalHistory,
  type EvalRun,
  type EvalVerdict,
  type EvalView,
  costText,
  loadEvals,
  runEvals,
  signed,
  stopEvals,
} from "./evalsApi";
import { type ProviderSettingsProps, failureText } from "./ProviderSettings";
import { dangerBtn, primaryBtn, smallBtn } from "./SettingsControls";

// How often a running check is asked how far it got, in milliseconds.
export const EVAL_POLL = 2000;

// The tasks, in the person's words. The gateway names them by id; one it names that is not here is
// shown under its own id.
const TASKS: Record<string, string> = {
  "create-file": "Create a file",
  "fix-typo": "Fix a typo",
  "count-lines": "Count lines",
  "rename-file": "Rename a file",
  "sort-lines": "Sort lines",
};

function taskName(id: string): string {
  return t(TASKS[id] || id);
}

// Why the last check no longer says whether the model is still fine.
const STALE: Record<string, string> = {
  never: "No check yet. Run one to know how the model does today.",
  model:
    "The model changed since the last check. Run it again to see if it got worse.",
  prompt:
    "The instructions the agent works with changed since the last check. Run it again to see if it got worse.",
  cases:
    "The tasks were updated since the last check, so it cannot be compared. Run it again.",
};

const HEADLINE: Record<string, string> = {
  first: "First check done. The next one will be compared with this one.",
  better: "Better than last time",
  same: "About the same as last time",
  worse: "Worse than last time",
};

// EvalSettings is the quality check of the models tab: a few fixed tasks run on the coding model,
// compared with the last run. It spends the model's quota, so it asks before it runs. `watch`
// changes when the models are saved, so the page notices the model is not the one last checked.
export function EvalSettings({
  api,
  onNotice,
  watch,
}: ProviderSettingsProps & { watch: string }) {
  const [view, setView] = useState<EvalView | null>(null);
  const [asking, setAsking] = useState(false);
  const [err, setErr] = useState("");
  const reload = useCallback(
    () =>
      loadEvals(api)
        .then((v) => setView(choose(v?.progress, v, null)))
        .catch(() => setView(null)),
    [api],
  );
  useEffect(() => {
    reload();
  }, [reload, watch]);
  const running = view?.progress.running;
  useEffect(() => {
    const id = running ? setInterval(reload, EVAL_POLL) : undefined;
    return () => clearInterval(id);
  }, [running, reload]);
  const act = async (fn: (api: Api) => Promise<unknown>, done?: string) => {
    setErr("");
    setAsking(false);
    try {
      await fn(api);
      if (done) {
        onNotice(done);
      }
    } catch (error_) {
      setErr(failureText(error_));
    }
    await reload();
  };
  if (!view) {
    return null;
  }
  let failure = err;
  if (!failure && view.progress.error) {
    failure = tf("The last check could not finish: {error}", {
      error: view.progress.error,
    });
  }
  return (
    <section class="settings-group" aria-labelledby="llm-evals-h">
      <h4 id="llm-evals-h" class="settings-group-title">
        {t("Quality check")}
      </h4>
      <div class="settings-card" data-testid="llm-evals">
        <div class="settings-add">
          <p class="text-xs text-[#7a7a8c]">
            {t(
              "Runs a few small fixed tasks and tells you if the model got worse than the last time. It uses part of your model's quota, so it only runs when you ask.",
            )}
          </p>
          <p class="text-xs text-[#e8e8ea] mt-2" data-testid="eval-model">
            {tf("Model checked: {model}", { model: view.model })}
          </p>
          {view.supported && <Stale stale={view.stale} />}
        </div>
        {choose(
          view.supported,
          <Controls
            view={view}
            asking={asking}
            setAsking={setAsking}
            act={act}
          />,
          <p
            class="settings-add text-xs text-warning"
            data-testid="eval-unsupported"
          >
            {t("This computer cannot run the check: it needs a command line.")}
          </p>,
        )}
        {failure && (
          <p class="settings-add text-xs text-danger" role="alert">
            {failure}
          </p>
        )}
        {view.last && view.verdict && (
          <Result last={view.last} v={view.verdict} history={view.history} />
        )}
      </div>
    </section>
  );
}

function Stale({ stale }: { stale: string }) {
  if (!STALE[stale]) {
    return null;
  }
  return (
    <p
      class="text-xs text-warning mt-2"
      data-testid="eval-stale"
      data-stale={stale}
    >
      {t(STALE[stale])}
    </p>
  );
}

// Controls is the run button with its confirmation, or the progress with its stop button.
function Controls({
  view,
  asking,
  setAsking,
  act,
}: {
  view: EvalView;
  asking: boolean;
  setAsking: (v: boolean) => void;
  act: (fn: (api: Api) => Promise<unknown>, done?: string) => Promise<void>;
}) {
  const p = view.progress;
  const stop = () => act(stopEvals);
  const start = () => act(runEvals, t("Check started"));
  const ask = () => setAsking(true);
  const dismiss = () => setAsking(false);
  if (p.running) {
    return (
      <div class="settings-add llm-actions" data-testid="eval-running">
        <output class="text-xs text-[#e8e8ea] flex-1 min-w-0">
          {tf("Running task {n} of {total}…", {
            n: Math.min(p.done + 1, p.total),
            total: p.total,
          })}
        </output>
        <button type="button" class={dangerBtn} onClick={stop}>
          {t("Stop")}
        </button>
      </div>
    );
  }
  if (asking) {
    return (
      <div class="settings-add llm-actions" data-testid="eval-confirm">
        <p class="text-xs text-[#e8e8ea] flex-1 min-w-0">
          {tf(
            "This runs {n} small tasks on {model} and uses part of your quota. Run it?",
            { n: view.cases.length, model: view.model },
          )}
        </p>
        <button type="button" class={primaryBtn} onClick={start}>
          {t("Run the check")}
        </button>
        <button type="button" class={smallBtn} onClick={dismiss}>
          {t("Cancel")}
        </button>
      </div>
    );
  }
  return (
    <div class="settings-add llm-actions">
      <button
        type="button"
        class={primaryBtn}
        data-testid="eval-run"
        onClick={ask}
      >
        {t("Run check")}
      </button>
    </div>
  );
}

// Result is the last run: how it compares with the one before, the tasks one by one, and the runs
// before it.
function Result({
  last,
  v,
  history,
}: {
  last: EvalRun;
  v: EvalVerdict;
  history: EvalHistory[];
}) {
  const s = last.summary;
  return (
    <div
      class="settings-add eval-result"
      data-testid="eval-result"
      data-status={v.status}
    >
      <output class={"eval-headline eval-" + v.status}>
        {t(HEADLINE[v.status] || HEADLINE.same)}
      </output>
      <p class="text-xs text-[#7a7a8c]">
        {tf("{model} · {passed} of {total} tasks passed", {
          model: last.model,
          passed: s.passed,
          total: s.total,
        })}
        {v.status !== "first" && ` (${signed(v.delta_passed)})`}
      </p>
      <p class="text-xs text-[#7a7a8c]" data-testid="eval-cost">
        {tf("Steps: {steps}", { steps: s.steps })}
        {v.status !== "first" && ` (${signed(v.delta_steps)})`} ·{" "}
        {choose(
          s.cost_known,
          tf("Cost: about {cost}", { cost: costText(s.cost_usd) }),
          t("Cost: unknown for this model"),
        )}
      </p>
      <ul class="eval-tasks">
        {last.results.map((r) => (
          <TaskLine key={r.id} r={r} broke={v.regressed.includes(r.id)} />
        ))}
      </ul>
      {history.length > 1 && (
        <details class="eval-history">
          <summary class="text-xs text-[#7a7a8c]">
            {t("Earlier checks")}
          </summary>
          <ul>
            {history.map((h) => (
              <li key={h.id} class="text-xs text-[#7a7a8c]">
                {tf("{date} · {model} · {passed} of {total}", {
                  date: h.at.slice(0, 10),
                  model: h.model,
                  passed: h.summary.passed,
                  total: h.summary.total,
                })}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

function TaskLine({ r, broke }: { r: EvalCaseResult; broke: boolean }) {
  return (
    <li class="eval-task" data-pass={String(r.pass)}>
      <span class="text-xs text-[#e8e8ea] flex-1 min-w-0">
        {taskName(r.id)}
      </span>
      {broke && <span class="text-xs text-danger">{t("Stopped working")}</span>}
      <span class={"text-xs " + choose(r.pass, "eval-pass", "text-danger")}>
        {choose(r.pass, t("Passed"), t("Failed"))}
      </span>
    </li>
  );
}
