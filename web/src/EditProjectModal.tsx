// The edit project modal: title, description, the main branch and how its pull requests are merged.
import { useCallback } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { branchChoices } from "./appFormat";
import { IconClose } from "./icons";
import type { EditProjectDraft } from "./appTypes";
import { EDIT_PROJECT, PR_MAX_FIXES_LIMIT } from "./appConst";
import { ReposSection } from "./ReposField";
import { connectedAccounts } from "./gitApi";
import { ProjectIndexPanel } from "./ProjectIndexPanel";

// The look of the dialog's fields.
const FIELD =
  "w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";
const HINT = "text-xs text-[#7a7a8c] mt-1";

type Patch = (patch: Partial<EditProjectDraft>) => void;

// fixesOf reads the number of attempts typed in the field, kept between 0 and the limit.
function fixesOf(value: string): number {
  const n = Math.round(Number(value));
  return choose(
    Number.isFinite(n),
    Math.min(PR_MAX_FIXES_LIMIT, Math.max(0, n)),
    0,
  );
}

// BranchFields are the main branch and the merge method.
function BranchFields({ p, patch }: { p: EditProjectDraft; patch: Patch }) {
  return (
    <>
      <div>
        <label class={LABEL} for="edit-project-main">
          {t("Main branch")}
        </label>
        <select
          id="edit-project-main"
          data-testid="edit-project-main"
          class={FIELD + " font-mono text-sm"}
          value={p.main}
          onChange={(e) =>
            patch({ main: (e.target as HTMLSelectElement).value })
          }
        >
          {branchChoices(p.branches, p.main).map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
        </select>
        <p class={HINT}>
          {t(
            "The project always goes back to this branch when it has no sessions.",
          )}
          {p.current && p.current !== p.main && (
            <> {tf("It is on {branch} now.", { branch: p.current })}</>
          )}
        </p>
      </div>
      <div>
        <label class={LABEL} for="edit-project-merge">
          {t("Merge method")}
        </label>
        <select
          id="edit-project-merge"
          data-testid="edit-project-merge"
          class={FIELD + " text-sm"}
          value={p.mergeMethod}
          onChange={(e) =>
            patch({ mergeMethod: (e.target as HTMLSelectElement).value })
          }
        >
          <option value="">{t("Merge commit")}</option>
          <option value="squash">{t("Squash")}</option>
          <option value="rebase">{t("Rebase")}</option>
        </select>
        <p class={HINT}>
          {t(
            "How a pull request of this project is merged from the PR bar. Its branch is deleted afterwards.",
          )}
        </p>
      </div>
    </>
  );
}

// PullRequestFields are what happens with the project's pull requests once the CI runs.
function PullRequestFields({
  p,
  patch,
}: {
  p: EditProjectDraft;
  patch: Patch;
}) {
  return (
    <>
      <div>
        <label class={LABEL} for="edit-project-fixes">
          {t("Attempts to fix a failing CI")}
        </label>
        <input
          id="edit-project-fixes"
          data-testid="edit-project-fixes"
          type="number"
          min="0"
          max="20"
          step="1"
          inputMode="numeric"
          class={FIELD + " text-sm"}
          value={String(p.prMaxFixes)}
          onInput={(e) =>
            patch({ prMaxFixes: fixesOf((e.target as HTMLInputElement).value) })
          }
        />
        <p class={HINT}>
          {t(
            "0 uses the default (5). After this many attempts Motita stops and asks you to take a look.",
          )}
        </p>
      </div>
      <label class="flex items-start gap-2.5 text-sm text-[#e8e8ea] cursor-pointer">
        <input
          type="checkbox"
          data-testid="edit-project-automerge"
          class="mt-1"
          checked={p.autoMerge}
          onChange={(e) =>
            patch({ autoMerge: (e.target as HTMLInputElement).checked })
          }
        />
        <span>
          {t("Merge automatically when the CI passes")}
          <span class="block text-xs text-[#7a7a8c]">
            {t(
              "Only when the host accepts the merge. Off, merging is always your click.",
            )}
          </span>
        </span>
      </label>
      <label class="flex items-start gap-2.5 text-sm text-[#e8e8ea] cursor-pointer">
        <input
          type="checkbox"
          data-testid="edit-project-autocontinue"
          class="mt-1"
          checked={p.autoContinue}
          onChange={(e) =>
            patch({ autoContinue: (e.target as HTMLInputElement).checked })
          }
        />
        <span>
          {t("Open a new session when a pull request is merged")}
          <span class="block text-xs text-[#7a7a8c]">
            {t(
              "It starts from the updated branch, so the next piece of work has somewhere to begin.",
            )}
          </span>
        </span>
      </label>
    </>
  );
}

// FolderField shows the folder the project is anchored to and, when Settings → Security allows the
// whole disk, lets it point at another one. Nothing is moved on disk.
function FolderField({ p, patch }: { p: EditProjectDraft; patch: Patch }) {
  const { fullDisk, openFolderPicker } = useApp().projects;
  const shown = p.path || p.dir;
  return (
    <div data-testid="edit-project-folder">
      <label class={LABEL}>{t("Folder")}</label>
      <p
        class="px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 font-mono text-xs text-[#e8e8ea] break-all"
        data-testid="edit-project-dir"
      >
        {shown}
      </p>
      {fullDisk && (
        <div class="flex gap-2 mt-2">
          <button
            type="button"
            class="px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5"
            data-testid="edit-project-browse"
            onClick={() => openFolderPicker("edit")}
          >
            {t("Choose another folder…")}
          </button>
          {p.path && (
            <button
              type="button"
              class="px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#9a9aaa] hover:bg-white/5"
              data-testid="edit-project-path-clear"
              onClick={() => patch({ path: "" })}
            >
              {t("Keep the current one")}
            </button>
          )}
        </div>
      )}
      {fullDisk && (
        <p class={HINT}>
          {t(
            "Nothing is moved: the project points at that folder. Not possible while it has sessions or other repositories.",
          )}
        </p>
      )}
    </div>
  );
}

// EditRepos are the project's repositories. They change at once, not on Save.
function EditRepos({ p }: { p: EditProjectDraft }) {
  const {
    repoBusy,
    repoConflict,
    addProjectRepo,
    removeProjectRepo,
    detachProjectMain,
    syncProjectRepo,
    standaloneProjectRepo,
    gitAccounts,
    openOtherRepoPicker,
    repoCatalog,
    fullDisk,
    openFolderPicker,
  } = useApp().projects;
  const connected = !!gitAccounts && connectedAccounts(gitAccounts).length > 0;
  const onRemove = useCallback(
    (name: string, force: boolean) => void removeProjectRepo(name, force),
    [removeProjectRepo],
  );
  return (
    <ReposSection
      main={p.mainRepo}
      repos={p.repos}
      busy={repoBusy}
      conflict={repoConflict}
      onRemove={onRemove}
      onAdd={addProjectRepo}
      onMoveMain={detachProjectMain}
      onSync={syncProjectRepo}
      onStandalone={standaloneProjectRepo}
      onChoose={choose(connected, openOtherRepoPicker, undefined)}
      onBrowse={choose(fullDisk, () => openFolderPicker("repo"), undefined)}
      catalog={repoCatalog}
      projectId={p.id}
      hint={t(
        "Changes here apply right away. Other projects that use a repository keep it.",
      )}
    />
  );
}

export function EditProjectModal({ p }: { p: EditProjectDraft }) {
  const {
    patchEditProject: patch,
    closeEditProject,
    savingProject,
    saveEditProject,
  } = useApp().projects;
  const { api } = useApp();
  return (
    <ModalBackdrop onClose={closeEditProject}>
      <dialog
        open
        class="app-dialog frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl max-h-[92vh] overflow-y-auto"
        aria-label={t(EDIT_PROJECT)}
      >
        <div class="flex items-center gap-2 mb-4">
          <h2 class="text-base font-semibold">{t(EDIT_PROJECT)}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeEditProject}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="space-y-4">
          <div>
            <label class={LABEL}>
              {t("Title")} <span class="text-danger">*</span>
            </label>
            <input
              class={FIELD}
              value={p.title}
              onInput={(e) =>
                patch({ title: (e.target as HTMLInputElement).value })
              }
              autoFocus
            />
          </div>
          <div>
            <label class={LABEL}>{t("Description (optional)")}</label>
            <input
              class={FIELD}
              value={p.description}
              onInput={(e) =>
                patch({ description: (e.target as HTMLInputElement).value })
              }
            />
          </div>
          <FolderField p={p} patch={patch} />
          <EditRepos p={p} />
          {api && <ProjectIndexPanel api={api} id={p.id} />}
          <BranchFields p={p} patch={patch} />
          <PullRequestFields p={p} patch={patch} />
        </div>
        <div class="flex gap-2 mt-5">
          <button
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
            onClick={() => void saveEditProject()}
            disabled={savingProject || !p.title.trim()}
          >
            {choose(savingProject, t("Saving…"), t("Save"))}
          </button>
          <button
            class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
            onClick={closeEditProject}
          >
            {t("Cancel")}
          </button>
        </div>
      </dialog>
    </ModalBackdrop>
  );
}
