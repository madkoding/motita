// The parts a turn of the conversation is drawn with: the input and its menus, the trail of steps,
// the line of what is happening now, and what a finished run changed.
import { useCallback, useRef, useState } from "preact/hooks";
import { t, tf, plural } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { useTurn } from "./turnView";
import { useDialog } from "./useDialog";
import { useListener } from "./domHooks";
import { ActionCount, ActionTrail } from "./ActionTrail";
import { BodyPortal } from "./BodyPortal";
import { FloatingMenu } from "./FloatingMenu";
import {
  classifyProgress,
  oneLine,
  progressText,
  switchedText,
  unsureWhy,
  whyParts,
  type TurnMode,
} from "./appParse";
import { lastExit, statusLabel } from "./appFormat";
import { RUN_MENU } from "./useCheckpoints";
import { type AttachedFile, attachedIn } from "./attachments";
import type { ChangeReport, Message, StepView } from "./appTypes";

// ShellStatus is all the conversation says about a command's result: still running, worked,
// or failed with which code. What it printed is in the terminal drawer.
export function ShellStatus({
  exit,
  settled = false,
}: {
  exit: number | null | undefined;
  settled?: boolean;
}) {
  // A command of a turn that has ended and never reported a result was cut off or refused.
  if (exit == null && settled) {
    return <span class="term-exit">{t("no result")}</span>;
  }
  if (exit == null) {
    return <span class="term-exit running">{t("running")}</span>;
  }
  return (
    <span class={`term-exit${choose(exit, " bad", "")}`}>
      {choose(exit, tf("failed · exit {code}", { code: exit }), "ok")}
    </span>
  );
}

// ChangesZoom is the full-size view of one preview: a click or Escape closes it. It holds nothing to
// focus, so the focus stays on the preview that opened it. It is drawn in a portal because its card
// sits inside a chat bubble (see BodyPortal).
function ChangesZoom({ url, onClose }: { url: string; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useDialog(ref, onClose);
  useListener(ref, "click", onClose);
  return (
    <BodyPortal>
      <dialog
        ref={ref}
        open
        data-dialog-root
        class="changes-zoom app-dialog"
        aria-modal="true"
        aria-label={t("preview")}
        tabIndex={-1}
      >
        <img src={url} alt={t("preview")} />
      </dialog>
    </BodyPortal>
  );
}

// FileList is the files a run changed, folded under a summary.
function FileList({ files }: { files: NonNullable<ChangeReport["files"]> }) {
  const added = files.reduce((n, f) => n + f.added, 0);
  const deleted = files.reduce((n, f) => n + f.deleted, 0);
  return (
    <details class="changes-files">
      <summary>
        {plural(files.length, "1 file changed", "{n} files changed")}
        <span class="chg-add"> +{added}</span>
        <span class="chg-del"> −{deleted}</span>
      </summary>
      <ul>
        {files.map((f) => (
          <li key={f.path}>
            <span class={`chg-tag ${f.status}`}>{statusLabel(f.status)}</span>
            <span class="chg-path">{f.path}</span>
            {(f.added > 0 || f.deleted > 0) && (
              <span class="chg-nums">
                <span class="chg-add">+{f.added}</span>{" "}
                <span class="chg-del">−{f.deleted}</span>
              </span>
            )}
          </li>
        ))}
      </ul>
    </details>
  );
}

// ChangesCard is what a finished run shows the user about its work: the pictures first, since
// that is what a person can judge, then the files as a folded summary. The code stays in the diff.
export function ChangesCard({ changes }: { changes: ChangeReport }) {
  const files = changes.files ?? [];
  const pics = changes.previews ?? [];
  const [zoom, setZoom] = useState<string | null>(null);
  const closeZoom = useCallback(() => setZoom(null), []);
  return (
    <div class="changes-card">
      {pics.length > 0 && (
        <div class="changes-previews">
          {pics.map((p) => (
            <figure key={p.name} class="changes-shot">
              <img src={p.url} alt={p.name} loading="lazy" />
              <figcaption>{p.name}</figcaption>
              <button
                type="button"
                class="row-hit"
                aria-label={p.name}
                onClick={() => setZoom(p.url)}
              />
            </figure>
          ))}
        </div>
      )}
      {files.length > 0 && <FileList files={files} />}
      {zoom && <ChangesZoom url={zoom} onClose={closeZoom} />}
    </div>
  );
}

// MenuDots is the ⋯ of an input's menu.
function MenuDots() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <circle cx="12" cy="5" r="1.8" />
      <circle cx="12" cy="12" r="1.8" />
      <circle cx="12" cy="19" r="1.8" />
    </svg>
  );
}

// TurnMenu is the one ⋮ of an input, everything that can be done with it in one place: stopping
// the run it started, sending it again in the other mode, and going back to its checkpoint, with
// or without the files. Putting the files back asks twice.
function TurnMenu({
  menuKey,
  liveHere,
  mode,
  text,
  turn,
}: {
  menuKey: number;
  liveHere: boolean;
  mode: TurnMode | null;
  text: string;
  turn: number | null | undefined;
}) {
  const { cpMenu, setCpMenu, toggleCpMenu, stopRun, submit } = useTurn();
  const open = cpMenu === menuKey;
  const btn = useRef<HTMLButtonElement>(null);
  return (
    <div class="cp-wrap">
      <button
        ref={btn}
        type="button"
        class="cp-btn"
        aria-label={t("Message menu")}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t("Message menu")}
        onClick={() => toggleCpMenu(menuKey)}
      >
        <MenuDots />
      </button>
      {open && (
        <FloatingMenu anchor={btn.current} class="cp-menu">
          {liveHere && (
            <button
              type="button"
              role="menuitem"
              class="is-armed"
              onClick={() => {
                setCpMenu(null);
                stopRun();
              }}
            >
              {t("Stop the agent")}
              <small>
                {t("A message typed meanwhile goes to the run; Esc stops it")}
              </small>
            </button>
          )}
          {mode && (
            <button
              type="button"
              role="menuitem"
              class="cp-mode"
              onClick={() => {
                setCpMenu(null);
                submit(switchedText(text, mode));
              }}
            >
              {choose(mode === "plan", t("Run as task"), t("Run as plan"))}
              <small>{t("sends this message again")}</small>
            </button>
          )}
          {turn != null && <CheckpointItems turn={turn} />}
        </FloatingMenu>
      )}
    </div>
  );
}

// CheckpointItems are the menu's ways back to an input's checkpoint: the conversation only, or
// the files too. Putting the files back asks twice.
function CheckpointItems({ turn }: { turn: number }) {
  const { cpArmed, setCpArmed, restoreCheckpoint, cpInfo } = useTurn();
  const files = cpInfo[turn]?.files;
  return (
    <>
      <button
        type="button"
        role="menuitem"
        onClick={() => restoreCheckpoint(turn, false)}
      >
        {t("Go back to this checkpoint")}
        <small>{t("conversation only")}</small>
      </button>
      <button
        type="button"
        role="menuitem"
        class={choose(cpArmed, "is-armed", "")}
        disabled={!files}
        title={choose(files, "", t("This checkpoint did not keep the files"))}
        onClick={(e) => {
          if (!cpArmed) {
            e.stopPropagation();
            setCpArmed(true);
            return;
          }
          restoreCheckpoint(turn, true);
        }}
      >
        {choose(
          cpArmed,
          t("Click again: discard changes since"),
          t("Go back and restore the changes"),
        )}
        <small>{t("conversation and files")}</small>
      </button>
    </>
  );
}

// UserText is what a message of the user's says. A message drawn under a label (the turn that
// applies suggestions) shows the label, and the prompt it sent opens under it on request.
export function UserText({ m }: { m: Message }) {
  const { lead, files } = attachedIn(m.text);
  if (files.length > 0) {
    return (
      <>
        <span>{lead}</span>
        <AttachedFiles files={files} />
      </>
    );
  }
  if (!m.shown) {
    return <span>{m.text}</span>;
  }
  return (
    <details class="msg-shown">
      <summary>{m.shown}</summary>
      <pre>{m.text}</pre>
    </details>
  );
}

// AttachedFiles are the files a message of the user's carries: a thumbnail for a picture, which
// opens whole in a tab, and the name for anything else.
function AttachedFiles({ files }: { files: AttachedFile[] }) {
  return (
    <div class="msg-files">
      {files.map((f) => (
        <FileThumb key={f.url} f={f} />
      ))}
    </div>
  );
}

function FileThumb({ f }: { f: AttachedFile }) {
  if (f.kind !== "image") {
    return <span class="msg-file">📎 {f.name}</span>;
  }
  return (
    <a href={f.url} target="_blank" rel="noopener" title={f.name}>
      <img class="msg-thumb" src={f.url} alt={f.name} loading="lazy" />
    </a>
  );
}

// TurnTop is the top of a turn: the input that opened it with the count of its actions in its
// bar, and their detail under it once opened. A turn opened by no input (the agent's first lines)
// has only the counter, on a line of its own.
export function TurnTop({
  head,
  turn,
  liveHere,
  mode,
  why = "",
  steps,
}: {
  head: Message | null;
  turn?: number;
  liveHere: boolean;
  mode: TurnMode | null;
  // why is the reason auto mode gave for the mode, "" when there is none.
  why?: string;
  steps: StepView[];
}) {
  const [open, setOpen] = useState(false);
  if (!head) {
    return choose(
      steps.length > 0,
      <Trail steps={steps} liveHere={liveHere} />,
      null,
    );
  }
  const actions = choose(
    steps.length > 0,
    { count: steps.length, open, onToggle: () => setOpen((o) => !o) },
    undefined,
  );
  return (
    <>
      <TurnHead
        head={head}
        turn={turn}
        liveHere={liveHere}
        mode={mode}
        why={why}
        actions={actions}
      />
      {open && actions && (
        <div class="activity-trail turn-steps" aria-label={t("steps")}>
          <TrailSteps steps={steps} liveHere={liveHere} />
        </div>
      )}
    </>
  );
}

// TurnHead is the input that opened a turn, sticky inside its group: a card whose top bar says
// who wrote it, the mode it ran in and how many actions it took, with its one menu at the end.
export function TurnHead({
  head,
  turn,
  liveHere,
  mode = null,
  why = "",
  actions,
}: {
  head: Message;
  turn?: number;
  liveHere: boolean;
  mode?: TurnMode | null;
  // why is the reason auto mode gave for the mode, "" when there is none.
  why?: string;
  // actions is the turn's action counter, drawn in the bar so it costs no row of its own.
  actions?: { count: number; open: boolean; onToggle: () => void };
}) {
  const { cpInfo, running, submit } = useTurn();
  const canGoBack = turn != null && cpInfo[turn] !== undefined && !running;
  const canSwitch = !!mode && !running;
  const switchMode = () => submit(switchedText(head.text, mode as TurnMode));
  // The live input's menu is RUN_MENU (it has no checkpoint yet); any other is keyed by its
  // turn, or, without one, by a key of its own below RUN_MENU.
  const menuKey = choose(liveHere, RUN_MENU, turn ?? RUN_MENU - 1 - head.id);
  return (
    <div class="turn-head">
      <div class="msg user turn-card">
        <div class="turn-bar">
          <span class="turn-who">{t("You")}</span>
          {mode && (
            <ModeBadge
              mode={mode}
              why={why}
              onSwitch={choose(canSwitch, switchMode, undefined)}
            />
          )}
          {actions && <ActionCount {...actions} />}
          {(liveHere || canGoBack || canSwitch) && (
            <TurnMenu
              menuKey={menuKey}
              liveHere={liveHere}
              mode={choose(canSwitch, mode, null)}
              text={head.text}
              turn={choose(canGoBack, turn, null)}
            />
          )}
        </div>
        <div class="turn-text">
          <UserText m={head} />
        </div>
      </div>
    </div>
  );
}

// planOutcome is the answer a plan turn ended with, when it is one the person can have carried
// out: text the agent wrote, not a card of questions or a report.
export function planOutcome(rest: Message[]): Message | null {
  const last = rest[rest.length - 1];
  const answer =
    last && last.role === "agent" && !last.kind && !last.draft && !last.report;
  return choose(
    answer && !last.questions && last.text.trim() !== "",
    last,
    null,
  );
}

// RunPlanBar closes a plan the agent wrote: a plan cannot run commands or change files, so the
// person is offered to have it carried out right there instead of writing the order again. It is
// offered only on the newest input, while nothing runs.
export function RunPlanBar({ head }: { head: Message }) {
  const { running, lastUserId, submit } = useTurn();
  if (running || head.id < lastUserId) {
    return null;
  }
  return (
    <div class="run-plan">
      <span class="run-plan-text">
        {t("This was a plan: nothing has been changed yet.")}
      </span>
      <button
        type="button"
        class="run-plan-btn"
        onClick={() => submit(t("run the plan"))}
      >
        {t("Run the plan now")}
      </button>
    </div>
  );
}

// ModeBadge says which mode auto mode picked for a turn, and why when the turn says. When the
// message can be sent again (no run is working) the badge is the switch itself: one tap runs it
// in the other mode, the same as the input's menu. A bare default (no rule and no model placed the
// message) is marked with a "?", since that guess is the one most worth a look.
export function ModeBadge({
  mode,
  why = "",
  onSwitch,
}: {
  mode: TurnMode;
  why?: string;
  onSwitch?: () => void;
}) {
  const unsure = unsureWhy(why);
  const label =
    choose(mode === "plan", t("Plan"), t("Task")) + choose(unsure, "?", "");
  const picked = choose(
    why !== "",
    tf("Auto mode picked it: {why}", {
      why: whyParts(why)
        .map((part) => t(part))
        .join("; "),
    }),
    t("The mode auto mode picked"),
  );
  if (!onSwitch) {
    return (
      <span
        class="mode-badge"
        data-mode={mode}
        data-unsure={choose(unsure, "", undefined)}
        title={picked}
      >
        {label}
      </span>
    );
  }
  const other = choose(mode === "plan", t("Run as task"), t("Run as plan"));
  return (
    <button
      type="button"
      class="mode-badge"
      data-mode={mode}
      data-unsure={choose(unsure, "", undefined)}
      title={picked + " · " + other}
      aria-label={label + " · " + other}
      onClick={onSwitch}
    >
      {label}
    </button>
  );
}

// TrailStep is one step of a turn's trail: a command with its result, or a line of progress.
function TrailStep({ step, settled }: { step: StepView; settled: boolean }) {
  if (step.kind === "command") {
    const cmd = step.text.replace(/^running:\s*/, "");
    return (
      <div class="trail-step command term-cmd" title={cmd}>
        <span class="term-cmd-text">{oneLine(cmd)}</span>
        <ShellStatus exit={step.exit} settled={settled} />
      </div>
    );
  }
  return (
    <div
      class={`trail-step ${classifyProgress(step.text) ?? ""}`}
      title={step.text}
    >
      {oneLine(progressText(step.text))}
    </div>
  );
}

// Trail is the steps a turn caused, folded behind a count.
export function Trail({
  steps,
  liveHere,
}: {
  steps: StepView[];
  liveHere: boolean;
}) {
  return (
    <ActionTrail count={steps.length}>
      <TrailSteps steps={steps} liveHere={liveHere} />
    </ActionTrail>
  );
}

// TrailSteps are the steps of a turn's trail, one line each.
function TrailSteps({
  steps,
  liveHere,
}: {
  steps: StepView[];
  liveHere: boolean;
}) {
  return (
    <>
      {steps.map((step) => (
        <TrailStep key={step.key} step={step} settled={!liveHere} />
      ))}
    </>
  );
}

// ActivityText is the activity line's text, shown whole as it changes: typing each new step in
// from the start made the line blank out and flicker while the agent moved quickly.
function ActivityText({ text, class: cls }: { text: string; class?: string }) {
  return (
    <span class={`activity-typed${choose(cls, " " + cls, "")}`}>{text}</span>
  );
}

// ActivityLine is what the agent is doing right now, below the turn it belongs to. It is ONE line,
// like the steps of the trail: a progress line can be the whole summary of the answer (reported
// when the run ends), and drawn whole it filled the "deciding the action" line with the text the
// voice reads aloud. The full line stays in the title.
export function ActivityLine({
  activity,
  shown,
}: {
  activity: string;
  shown: StepView[];
}) {
  const { activityKind } = useTurn();
  const command = activityKind === "command";
  return (
    <div class={`msg activity ${activityKind}`} title={activity}>
      <span class={`spin-${activityKind || "default"}`}></span>
      {command && (
        <>
          <ActivityText
            class="term-cmd-text"
            text={oneLine(activity.replace(/^running:\s*/, ""))}
          />
          <ShellStatus exit={lastExit(shown)} />
        </>
      )}
      {!command && <ActivityText text={oneLine(progressText(activity))} />}
    </div>
  );
}

// SubagentsStrip is the run's background agents at work, in the conversation: without it a main
// agent waiting on three others looked like one line of "waiting...", and the list was behind a tab.
// A click opens that list. It is not drawn while the list is open, or when nothing is running.
export function SubagentsStrip() {
  const { rs, drawers } = useApp();
  const running = rs.agents.filter((a) => a.parent && a.state === "running");
  if (running.length === 0 || drawers.agentsOpen) {
    return null;
  }
  return (
    <button
      type="button"
      class="subagents-strip"
      onClick={drawers.toggleAgents}
    >
      <span class="subagents-count">
        {plural(
          running.length,
          "1 background agent running",
          "{n} background agents running",
        )}
      </span>
      {running.map((a) => (
        <span key={a.id} class="subagents-chip" title={a.purpose}>
          {oneLine(a.purpose || a.id, 32)}
        </span>
      ))}
    </button>
  );
}

// RunArtifacts are the files a run saved, each opening over the session's artifact list.
export function RunArtifacts({
  artifacts,
}: {
  artifacts: NonNullable<Message["artifacts"]>;
}) {
  const { showRunArtifact } = useTurn();
  return (
    <div class="mt-2 flex flex-wrap gap-1.5" data-testid="run-artifacts">
      {artifacts.map((a) => (
        <button
          key={a.name}
          class="px-2.5 min-h-[32px] rounded-lg border border-white/10 bg-black/20 text-xs font-mono text-accent hover:bg-white/5"
          onClick={() => showRunArtifact(a)}
        >
          {a.name}
        </button>
      ))}
    </div>
  );
}
