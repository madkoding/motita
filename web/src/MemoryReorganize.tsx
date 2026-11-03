// The window of the reorganization of the memory: what it will do and that a copy is kept, how far
// it is while it runs, and what it did, with the way to undo it.
import { plural, t, tf } from "./i18n";
import { useApp } from "./appStore";
import { BodyPortal } from "./BodyPortal";
import { ModalBackdrop } from "./ModalBackdrop";
import { reportLines, stepLabel, type ReorgStatus } from "./memoryKinds";

const PRIMARY =
  "flex-1 min-h-[44px] px-5 rounded-xl bg-accent/80 text-white font-semibold active:scale-95 transition-transform";
const SECONDARY =
  "min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform";

// What the reorganization does, in the order it does it.
const WHAT_IT_DOES = [
  "Sorts each entry by kind: facts, what happened, decisions, your preferences, people, how it is done and project lessons.",
  "Merges entries that say the same thing, and gives a clear title to the ones without one.",
  "Ties a newer fact to the one it replaces, and marks entries that contradict each other.",
  "Associates entries about the same thing, so the map shows how they relate.",
  "Moves each project's lessons into that project's own collection.",
];

function Ask({
  onStart,
  onCancel,
}: {
  onStart: () => void;
  onCancel: () => void;
}) {
  return (
    <>
      <ul class="text-sm text-[#c8c8d2] list-disc pl-5 space-y-1 mb-3">
        {WHAT_IT_DOES.map((line) => (
          <li key={line}>{t(line)}</li>
        ))}
      </ul>
      <p class="text-sm text-[#9a9aaa] mb-5">
        {t("A copy of your memory is kept first, so you can undo it all.")}
      </p>
      <div class="flex gap-2">
        <button class={PRIMARY} data-testid="reorg-start" onClick={onStart}>
          {t("Reorganize")}
        </button>
        <button class={SECONDARY} data-testid="reorg-cancel" onClick={onCancel}>
          {t("Cancel")}
        </button>
      </div>
    </>
  );
}

function Running({
  status,
  onStop,
}: {
  status: ReorgStatus;
  onStop: () => void;
}) {
  const { done, total, step } = status.progress;
  const share = total > 0 ? Math.round((done / total) * 100) : 0;
  return (
    <>
      <p class="text-sm text-[#e8e8ea] mb-2" data-testid="reorg-step">
        {t(stepLabel(step))}
      </p>
      <div
        class="rounded-full bg-white/10 overflow-hidden mb-1"
        style={{ height: "6px" }}
        role="progressbar"
        aria-valuenow={share}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div class="h-full bg-accent" style={{ width: share + "%" }} />
      </div>
      {total > 0 && (
        <p class="text-xs text-[#9a9aaa] mb-5">
          {tf("{done} of {total}", { done, total })}
        </p>
      )}
      <button class={SECONDARY} data-testid="reorg-stop" onClick={onStop}>
        {t("Stop")}
      </button>
    </>
  );
}

function Done({
  status,
  onUndo,
  onClose,
}: {
  status: ReorgStatus;
  onUndo: () => void;
  onClose: () => void;
}) {
  const report = status.report as NonNullable<ReorgStatus["report"]>;
  const lines = reportLines(report);
  return (
    <>
      {!!status.error && (
        <p class="text-sm text-warning mb-2" data-testid="reorg-error">
          {t(
            "It stopped before the end. What was done is kept, and Undo takes it back.",
          )}
        </p>
      )}
      {lines.length === 0 ? (
        <p class="text-sm text-[#e8e8ea] mb-2">
          {t("Everything was already in order.")}
        </p>
      ) : (
        <ul
          class="text-sm text-[#e8e8ea] list-disc pl-5 space-y-1 mb-2"
          data-testid="reorg-report"
        >
          {lines.map((l) => (
            <li key={l.key}>{tf(l.key, { n: l.n })}</li>
          ))}
        </ul>
      )}
      {!report.used_model && (
        <p class="text-xs text-[#9a9aaa] mb-2">
          {t(
            "No model was available, so only the lessons were moved and close entries tied.",
          )}
        </p>
      )}
      {report.unanswered > 0 && (
        <p class="text-xs text-[#9a9aaa] mb-2">
          {plural(
            report.unanswered,
            "The model did not answer 1 time; those entries stay as they were.",
            "The model did not answer {n} times; those entries stay as they were.",
          )}
        </p>
      )}
      <p class="text-xs text-[#9a9aaa] mb-5">
        {tf("{n} entries in your memory now.", { n: report.entries })}
      </p>
      <div class="flex gap-2">
        <button class={PRIMARY} data-testid="reorg-close" onClick={onClose}>
          {t("Done")}
        </button>
        <button class={SECONDARY} data-testid="reorg-undo" onClick={onUndo}>
          {t("Undo")}
        </button>
      </div>
    </>
  );
}

// Body is what the window shows: the question, how far it is, or what it did.
function Body() {
  const { status, hide, start, stop, undo } = useApp().memory.reorg;
  if (status?.running) {
    return <Running status={status} onStop={() => void stop()} />;
  }
  if (status?.report) {
    return <Done status={status} onUndo={() => void undo()} onClose={hide} />;
  }
  return <Ask onStart={() => void start()} onCancel={hide} />;
}

// ReorganizeDialog is the window, shown while the memory's reorganization is open.
export function ReorganizeDialog() {
  const { failed, hide } = useApp().memory.reorg;
  return (
    <BodyPortal>
      <ModalBackdrop
        onClose={hide}
        class="fixed inset-0 bg-black/60 backdrop-blur-xs z-[56] flex items-center justify-center p-4"
      >
        <div
          class="frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl"
          data-testid="reorg-dialog"
        >
          <h2 class="text-base font-semibold mb-3">
            {t("Reorganize the memory")}
          </h2>
          <Body />
          {failed !== "" && (
            <p class="text-sm text-danger mt-3" data-testid="reorg-failed">
              {failed}
            </p>
          )}
        </div>
      </ModalBackdrop>
    </BodyPortal>
  );
}
