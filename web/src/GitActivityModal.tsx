// The modal that shows what a git action chosen in a menu is doing, and how it ended.
import { t } from "./i18n";
import { choose } from "./choose";
import { ModalBackdrop } from "./ModalBackdrop";
import { IconAlert, IconCheck, IconClose, IconSpinner } from "./icons";
import { closeGitActivity, useGitActivity } from "./gitActivity";

export function GitActivityModal() {
  const a = useGitActivity();
  if (!a) {
    return null;
  }
  return (
    <ModalBackdrop onClose={closeGitActivity}>
      <div
        class="frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl"
        data-testid="git-activity"
        role="dialog"
        aria-label={a.title}
      >
        <div class="flex items-center gap-2 mb-3">
          <h2 class="text-base font-semibold">{a.title}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeGitActivity}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <p
          class="font-mono text-xs text-[#9a9aaa] break-words"
          data-testid="git-activity-detail"
        >
          {a.detail}
        </p>
        <div
          class="flex items-start gap-2 mt-4 text-sm"
          data-testid="git-activity-state"
          aria-live="polite"
        >
          {a.state === "running" && (
            <>
              <IconSpinner class="animate-spin text-accent" size={16} />
              <span>{t("Working…")}</span>
            </>
          )}
          {a.state !== "running" && (
            <>
              {choose(
                a.state === "done",
                <IconCheck class="text-[#40f0a0] flex-none" size={16} />,
                <IconAlert class="text-warning flex-none" size={16} />,
              )}
              <span class="min-w-0 break-words">{a.message}</span>
            </>
          )}
        </div>
        {a.output && (
          <pre
            class="mt-3 max-h-48 overflow-auto rounded-lg bg-black/30 border border-white/10 p-2 font-mono text-xs text-[#c8c8d4] whitespace-pre-wrap break-words"
            data-testid="git-activity-output"
          >
            {a.output}
          </pre>
        )}
      </div>
    </ModalBackdrop>
  );
}
