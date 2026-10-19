// The dialog that records one more repository: a URL to clone, or a folder the machine already has.
import { t } from "./i18n";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { IconClose, IconRepo, IconSpinner } from "./icons";

const FIELD =
  "w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] font-mono text-sm focus:outline-hidden focus:border-accent";
const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";

export function NewRepositoryModal() {
  const r = useApp().repositories;
  const empty = !r.newRepoUrl.trim() && !r.newRepoPath.trim();
  return (
    <ModalBackdrop onClose={r.closeNewRepository}>
      <dialog
        open
        class="app-dialog frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl max-h-[92vh] overflow-y-auto"
        aria-label={t("New repository")}
      >
        <div class="flex items-center gap-2 mb-4">
          <IconRepo size={20} class="text-accent" />
          <h2 class="text-base font-semibold">{t("New repository")}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={r.closeNewRepository}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="space-y-4">
          <div>
            <label class={LABEL} for="new-repo-url">
              {t("Repository URL")}
            </label>
            <input
              id="new-repo-url"
              data-testid="new-repo-url"
              class={FIELD}
              value={r.newRepoUrl}
              onInput={(e) =>
                r.setNewRepoUrl((e.target as HTMLInputElement).value)
              }
              placeholder={t("https://github.com/user/repo.git")}
              autoFocus
            />
          </div>
          <div>
            <label class={LABEL} for="new-repo-path">
              {t("or a folder on this machine")}
            </label>
            <input
              id="new-repo-path"
              data-testid="new-repo-path"
              class={FIELD}
              value={r.newRepoPath}
              onInput={(e) =>
                r.setNewRepoPath((e.target as HTMLInputElement).value)
              }
              placeholder="/home/me/code/repo"
            />
            <p class="text-xs text-[#7a7a8c] mt-1">
              {t(
                "The repository is cloned once. A folder is used where it is and never deleted.",
              )}
            </p>
          </div>
        </div>
        {r.newRepoError && (
          <p class="mt-3 text-sm text-danger" data-testid="new-repo-error">
            {r.newRepoError}
          </p>
        )}
        <div class="flex gap-2 mt-5">
          <button
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
            data-testid="new-repo-create"
            onClick={() => void r.createRepository()}
            disabled={r.creatingRepository || empty}
          >
            {r.creatingRepository && (
              <IconSpinner
                class="animate-spin"
                size={16}
                style="display:inline-block;vertical-align:middle;margin-right:6px"
              />
            )}
            {t("Add repository")}
          </button>
          <button
            class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
            onClick={r.closeNewRepository}
          >
            {t("Cancel")}
          </button>
        </div>
      </dialog>
    </ModalBackdrop>
  );
}
