import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { fullUpdated } from "./appFormat";
import {
  type AutopilotChange,
  type AutopilotView,
  type Routine,
  type RoutineInfo,
  getAutopilot,
  runAutopilot,
  setAutopilot,
} from "./autopilotApi";
import { Row, Segmented, inputCls, primaryBtn } from "./SettingsControls";

type OnOff = "on" | "off";

const MIN_OPEN = 1;
const MAX_OPEN = 10;

// useAutopilot holds the section's state: what the gateway reported and the actions that change it.
function useAutopilot(api: Api) {
  const [view, setView] = useState<AutopilotView | null | undefined>(undefined);
  const [loadErr, setLoadErr] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    getAutopilot(api)
      .then((v) => live && setView(v))
      .catch((e) => live && setLoadErr(errorMessage(e)));
    return () => {
      live = false;
    };
  }, [api]);

  // act runs one request and shows what it answered, or why it was refused.
  const act = useCallback(async (request: () => Promise<AutopilotView>) => {
    setBusy(true);
    setErr("");
    try {
      setView(await request());
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  }, []);
  const save = useCallback(
    (change: AutopilotChange) => act(() => setAutopilot(api, change)),
    [api, act],
  );
  const run = useCallback(
    (what: Routine) => act(() => runAutopilot(api, what)),
    [api, act],
  );
  return { view, loadErr, err, busy, save, run };
}

// RoutineStatus says when a routine last ran and how it ended, and when it runs next.
function RoutineStatus({
  info,
  last,
  testid,
}: {
  info: RoutineInfo | null;
  last: string;
  testid: string;
}) {
  if (!info) {
    return null;
  }
  const lines: string[] = [];
  const ran = fullUpdated(info.last_run);
  if (ran) {
    const outcome = info.last_outcome;
    lines.push(
      tf(last, { date: ran }) + choose(outcome !== "", " · " + outcome, ""),
    );
  }
  const next = fullUpdated(info.next_run);
  if (next) {
    lines.push(tf("Next: {date}", { date: next }));
  }
  if (lines.length === 0) {
    return null;
  }
  return (
    <p class="settings-card-note text-xs text-[#7a7a8c]" data-testid={testid}>
      {lines.join(" · ")}
    </p>
  );
}

// RoutineRow is one switch with its "Run now" button and the line saying how it went.
function RoutineRow({
  id,
  label,
  hint,
  runLabel,
  last,
  field,
  routine,
  view,
  busy,
  save,
  run,
}: {
  id: string;
  label: string;
  hint: string;
  runLabel: string;
  last: string;
  field: "weekly_audit" | "auto_pr";
  routine: Routine;
  view: AutopilotView;
  busy: boolean;
  save: (c: AutopilotChange) => Promise<void>;
  run: (what: Routine) => Promise<void>;
}) {
  const on = view[field];
  const onChange = useCallback(
    (v: OnOff) => void save({ [field]: v === "on" }),
    [field, save],
  );
  const start = useCallback(() => void run(routine), [routine, run]);
  return (
    <>
      <Row id={id} label={label} hint={hint} stack>
        <div class="flex min-w-0 flex-1 flex-col gap-2">
          <Segmented<OnOff>
            name={id}
            labelledBy={id}
            value={choose(on, "on", "off")}
            onChange={onChange}
            disabled={!view.can_save || busy}
            options={[
              { value: "on", label: t("On") },
              { value: "off", label: t("Off") },
            ]}
          />
          <button
            type="button"
            class={primaryBtn + " self-start"}
            disabled={!on || busy}
            aria-label={runLabel}
            onClick={start}
            data-testid={"autopilot-run-" + routine}
          >
            {t("Run now")}
          </button>
        </div>
      </Row>
      <RoutineStatus
        info={view[routine]}
        last={last}
        testid={"autopilot-status-" + routine}
      />
    </>
  );
}

// LimitRow is how many pull requests of its own Motita keeps open at once.
function LimitRow({
  view,
  busy,
  save,
}: {
  view: AutopilotView;
  busy: boolean;
  save: (c: AutopilotChange) => Promise<void>;
}) {
  const onChange = useCallback(
    (e: Event) => {
      const n = Math.round(Number((e.currentTarget as HTMLInputElement).value));
      void save({
        max_open_prs: Math.min(MAX_OPEN, Math.max(MIN_OPEN, n || MIN_OPEN)),
      });
    },
    [save],
  );
  return (
    <Row
      id="settings-autopilot-limit"
      label={t("Most open pull requests")}
      hint={t(
        "Motita keeps at most this many of its own pull requests open at once, from 1 to 10. It opens no more until you merge or close some.",
      )}
      stack
    >
      <input
        key={view.max_open_prs}
        class={inputCls}
        type="number"
        min={MIN_OPEN}
        max={MAX_OPEN}
        step="1"
        aria-labelledby="settings-autopilot-limit"
        value={view.max_open_prs}
        disabled={!view.auto_pr || !view.can_save || busy}
        onChange={onChange}
        data-testid="autopilot-limit"
      />
    </Row>
  );
}

// AutopilotSettings is the upkeep tab: a weekly check of each project that touches no code, and
// draft pull requests for small, safe findings. Both start off. The section stays hidden on a
// gateway without the upkeep API.
export function AutopilotSettings({ api }: { api: Api }) {
  const { view, loadErr, err, busy, save, run } = useAutopilot(api);
  if (view === null || (view === undefined && !loadErr)) {
    return null;
  }
  return (
    <section class="settings-group" aria-labelledby="settings-autopilot-h">
      <h3 id="settings-autopilot-h" class="settings-group-title">
        {t("Upkeep")}
      </h3>
      <div class="settings-card" data-testid="autopilot-setting">
        {loadErr && (
          <p class="settings-card-note text-xs text-danger" role="alert">
            {loadErr}
          </p>
        )}
        {view && (
          <>
            <RoutineRow
              id="settings-autopilot-audit"
              label={t("Weekly check")}
              hint={t(
                "Once a week Motita looks over each project without changing anything and tells you what is new. It touches no code.",
              )}
              runLabel={t("Run the weekly check now")}
              last="Last check: {date}"
              field="weekly_audit"
              routine="audit"
              view={view}
              busy={busy}
              save={save}
              run={run}
            />
            <RoutineRow
              id="settings-autopilot-prs"
              label={t("Draft pull requests")}
              hint={t(
                "For small, safe findings, such as texts, missing tests and code-quality warnings, Motita opens a draft pull request in the project's own repository. It never changes the main branch and never merges.",
              )}
              runLabel={t("Look for small fixes now")}
              last="Last round of fixes: {date}"
              field="auto_pr"
              routine="fixes"
              view={view}
              busy={busy}
              save={save}
              run={run}
            />
            <LimitRow view={view} busy={busy} save={save} />
            {!view.can_save && (
              <p class="settings-card-note text-xs text-[#7a7a8c]">
                {t(
                  "The gateway runs on its built-in defaults, without a configuration file, so this cannot be changed here.",
                )}
              </p>
            )}
          </>
        )}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="autopilot-error"
          >
            {err}
          </p>
        )}
      </div>
    </section>
  );
}
