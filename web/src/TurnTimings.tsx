// How a finished turn divided its time, drawn as one quiet line under the answer: where a slow
// turn was slow (the model, the commands, a question waiting for you, the project's checks)
// without opening the gateway's log.
import { t, tf } from "./i18n";

export interface TurnTimings {
  total_ms: number;
  model_ms?: number;
  model_calls?: number;
  commands_ms?: number;
  commands?: number;
  approval_wait_ms?: number;
  approvals_asked?: number;
  approvals_reused?: number;
  anchor_ms?: number;
}

const SECOND = 1000;
const MINUTE = 60 * SECOND;

// seconds is a duration as a person reads it: "850 ms", "4.2 s", "3 min 05 s".
export function seconds(ms: number): string {
  if (ms < SECOND) {
    return `${Math.round(ms)} ms`;
  }
  if (ms < MINUTE) {
    return `${(ms / SECOND).toFixed(1)} s`;
  }
  const rest = Math.round((ms % MINUTE) / SECOND);
  return `${Math.floor(ms / MINUTE)} min ${String(rest).padStart(2, "0")} s`;
}

// timingParts are the parts of a turn worth naming: the ones it spent time on, with their counts.
export function timingParts(tm: TurnTimings): string[] {
  const parts = [
    { ms: tm.model_ms, n: tm.model_calls, label: "model {time} ({n} calls)" },
    { ms: tm.commands_ms, n: tm.commands, label: "commands {time} ({n})" },
    {
      ms: tm.approval_wait_ms,
      n: tm.approvals_asked,
      label: "waiting for you {time} ({n})",
    },
    { ms: tm.anchor_ms, n: 0, label: "checks {time}" },
  ];
  const out = parts
    .filter((p) => (p.ms ?? 0) > 0)
    .map((p) =>
      tf(p.label, { time: seconds(p.ms as number), n: String(p.n ?? 0) }),
    );
  if ((tm.approvals_reused ?? 0) > 0) {
    out.push(
      tf("{n} approved earlier in the turn", {
        n: String(tm.approvals_reused),
      }),
    );
  }
  return out;
}

// hasTimings says a payload carries timings worth drawing.
export function hasTimings(tm: unknown): tm is TurnTimings {
  return (
    !!tm && typeof tm === "object" && ((tm as TurnTimings).total_ms ?? 0) > 0
  );
}

export function TurnTimingsLine({ timings }: { timings: TurnTimings }) {
  const parts = timingParts(timings);
  return (
    <div class="turn-timings" title={t("Where this turn's time went")}>
      {[seconds(timings.total_ms), ...parts].join(" · ")}
    </div>
  );
}
