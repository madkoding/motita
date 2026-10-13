// The parts a turn of the conversation is drawn with: the input and its menus, the trail of steps,
// the line of what is happening now, and what a finished run changed.
import { useCallback, useRef, useState } from "preact/hooks";
import { t, tf, plural } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { useDialog } from "./useDialog";
import { useListener } from "./domHooks";
import { ActionTrail } from "./ActionTrail";
import { classifyProgress, oneLine, progressText } from "./appParse";
import { lastExit, statusLabel } from "./appFormat";
import { RUN_MENU } from "./useCheckpoints";
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
// focus, so the focus stays on the preview that opened it.
function ChangesZoom({ url, onClose }: { url: string; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useDialog(ref, onClose);
  useListener(ref, "click", onClose);
  return (
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

// RunMenu is the menu of the input being worked on: it stops the agent.
function RunMenu() {
  const { checkpoints, stream } = useApp();
  const { cpMenu, setCpMenu, toggleCpMenu } = checkpoints;
  return (
    <div class="cp-wrap">
      <button
        type="button"
        class="cp-btn"
        aria-label={t("Run menu")}
        aria-haspopup="menu"
        aria-expanded={cpMenu === RUN_MENU}
        title={t("Run menu")}
        onClick={() => toggleCpMenu(RUN_MENU)}
      >
        <MenuDots />
      </button>
      {cpMenu === RUN_MENU && (
        <div class="cp-menu" role="menu">
          <button
            type="button"
            role="menuitem"
            class="is-armed"
            onClick={() => {
              setCpMenu(null);
              stream.stopRun();
            }}
          >
            {t("Stop the agent")}
            <small>
              {t("Enter sends a message that interrupts; Alt+Enter queues it")}
            </small>
          </button>
        </div>
      )}
    </div>
  );
}

// CheckpointMenu is the menu of an input that made a checkpoint: going back to it, with or without
// the files. Putting the files back asks twice.
function CheckpointMenu({ turn }: { turn: number }) {
  const { checkpoints, rs } = useApp();
  const { cpMenu, cpArmed, setCpArmed, toggleCpMenu, restoreCheckpoint } =
    checkpoints;
  const files = rs.cpInfo[turn]?.files;
  return (
    <div class="cp-wrap">
      <button
        type="button"
        class="cp-btn"
        aria-label={t("Checkpoint menu")}
        aria-haspopup="menu"
        aria-expanded={cpMenu === turn}
        title={t("Checkpoint")}
        onClick={() => toggleCpMenu(turn)}
      >
        <MenuDots />
      </button>
      {cpMenu === turn && (
        <div class="cp-menu" role="menu">
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
            title={choose(
              files,
              "",
              t("This checkpoint did not keep the files"),
            )}
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
        </div>
      )}
    </div>
  );
}

// UserText is what a message of the user's says. A message drawn under a label (the turn that
// applies suggestions) shows the label, and the prompt it sent opens under it on request.
export function UserText({ m }: { m: Message }) {
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

// TurnHead is the input that opened a turn, sticky inside its group, with its menus.
export function TurnHead({
  head,
  turn,
  liveHere,
}: {
  head: Message;
  turn?: number;
  liveHere: boolean;
}) {
  const { rs } = useApp();
  const canGoBack =
    turn != null && rs.cpInfo[turn] !== undefined && !rs.running;
  return (
    <div class="turn-head">
      <div class="msg user" data-who={t("You")}>
        <UserText m={head} />
      </div>
      {liveHere && <RunMenu />}
      {canGoBack && <CheckpointMenu turn={turn} />}
    </div>
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
      {steps.map((step) => (
        <TrailStep key={step.key} step={step} settled={!liveHere} />
      ))}
    </ActionTrail>
  );
}

// ActivityLine is what the agent is doing right now, below the turn it belongs to.
export function ActivityLine({
  activity,
  shown,
}: {
  activity: string;
  shown: StepView[];
}) {
  const { activityKind } = useApp().rs;
  const command = activityKind === "command";
  return (
    <div
      class={`msg activity ${activityKind}`}
      style={{ whiteSpace: "pre-wrap" }}
    >
      <span class={`spin-${activityKind || "default"}`}></span>
      {command && (
        <>
          <span class="term-cmd-text">
            {oneLine(activity.replace(/^running:\s*/, ""))}
          </span>
          <ShellStatus exit={lastExit(shown)} />
        </>
      )}
      {!command && progressText(activity)}
    </div>
  );
}

// RunArtifacts are the files a run saved, each opening over the session's artifact list.
export function RunArtifacts({
  artifacts,
}: {
  artifacts: NonNullable<Message["artifacts"]>;
}) {
  const { showRunArtifact } = useApp().artifacts;
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
