// The pinned plan above the composer: the plan the agent is working from, kept word for word
// through compactions and restarts. Folded to one line; opened, it can be changed or dropped.
import { useState } from "preact/hooks";
import { t } from "./i18n";
import { useApp } from "./appStore";
import { usePinnedPlan } from "./usePinnedPlan";

const BTN =
  "px-3 min-h-[34px] rounded-lg border border-white/10 text-xs text-[#c8c8d2] hover:bg-white/5 active:scale-95 transition-transform";

// firstStep is the plan's first line with words, for the folded bar.
export function firstStep(plan: string): string {
  return (
    plan
      .split("\n")
      .map((l) => l.trim())
      .find((l) => l !== "") ?? ""
  );
}

// PinnedPlanBar is the bar for the conversation on screen.
export function PinnedPlanBar() {
  const { api, rs, status } = useApp();
  const pinnedPlan = usePinnedPlan(
    api,
    rs.sessionId,
    rs.running,
    status.setState,
  );
  return <PinnedPlanView pinnedPlan={pinnedPlan} />;
}

// PinnedPlanView draws a plan and its editor.
export function PinnedPlanView({
  pinnedPlan,
}: {
  pinnedPlan: ReturnType<typeof usePinnedPlan>;
}) {
  const label = t("Active plan");
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");
  if (pinnedPlan.plan === "") {
    return null;
  }
  const edit = () => {
    setDraft(pinnedPlan.plan);
    setOpen(true);
  };
  const keep = async (text: string) => {
    if (await pinnedPlan.save(text)) {
      setOpen(false);
    }
  };
  return (
    <section
      class="mx-3 mb-2 rounded-xl border border-accent/30 bg-accent/5 px-3 py-2 text-sm"
      data-testid="pinned-plan"
      aria-label={label}
    >
      {!open && (
        <button
          type="button"
          class="w-full flex items-center gap-2 text-left min-h-[34px]"
          title={t(
            "The plan the agent keeps word for word. Open it to change or drop it.",
          )}
          data-testid="pinned-plan-open"
          onClick={edit}
        >
          <span class="flex-none text-accent">📌 {label}</span>
          <span class="truncate text-[#9a9aaa]">
            {firstStep(pinnedPlan.plan)}
          </span>
        </button>
      )}
      {open && (
        <div class="flex flex-col gap-2">
          <textarea
            class="w-full min-h-[140px] rounded-lg bg-black/30 border border-white/10 p-2 text-sm text-[#e8e8ea] font-mono"
            value={draft}
            aria-label={label}
            data-testid="pinned-plan-text"
            onInput={(e) => setDraft((e.target as HTMLTextAreaElement).value)}
          />
          <div class="flex flex-wrap gap-2 justify-end">
            <button
              type="button"
              class={BTN + " text-red-300"}
              data-testid="pinned-plan-drop"
              onClick={() => void keep("")}
            >
              {t("Drop the plan")}
            </button>
            <button
              type="button"
              class={BTN}
              data-testid="pinned-plan-cancel"
              onClick={() => setOpen(false)}
            >
              {t("Cancel")}
            </button>
            <button
              type="button"
              class="px-4 min-h-[34px] rounded-lg bg-accent text-white text-xs active:scale-95 transition-transform"
              data-testid="pinned-plan-save"
              onClick={() => void keep(draft)}
            >
              {t("Save")}
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
