// The new project modal. A project is a plain folder: it asks for a name and, if wanted, where the
// folder is. The repositories it works on are recorded apart (see NewRepositoryModal) and linked to
// the project afterwards.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { slugOf } from "./appParse";
import { podmanNote } from "./appFormat";
import { IconClose, IconFolder, IconSpinner } from "./icons";
import { NEW_PROJECT } from "./appConst";

// The look of the dialog's fields.
const FIELD =
  "w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";
const MONO_FIELD = FIELD + " font-mono text-sm";

// PodmanOffer asks whether the project runs with podman, when the machine has it.
function PodmanOffer() {
  const {
    newProjectPodman,
    setNewProjectPodman,
    podmanReady,
    hasPodmanCompose,
  } = useApp().projects;
  return (
    <label
      class="flex items-start gap-2.5 p-2.5 rounded-xl border border-white/10 bg-black/20 cursor-pointer"
      data-testid="podman-offer"
    >
      <input
        type="checkbox"
        class="mt-0.5 accent-(--accent,#7c6cff)"
        checked={newProjectPodman}
        onChange={(e) =>
          setNewProjectPodman((e.target as HTMLInputElement).checked)
        }
      />
      <span class="text-sm text-[#e8e8ea]">
        {t("Podman was found on this machine. Use it to run this project?")}
        <span class="block text-xs text-[#7a7a8c] mt-0.5">
          {podmanNote(podmanReady, hasPodmanCompose)}
        </span>
      </span>
    </label>
  );
}

// NameField is the project's name.
function NameField() {
  const p = useApp().projects;
  return (
    <div>
      <label class={LABEL}>
        {t("Name")} <span class="text-danger">*</span>
      </label>
      <input
        class={FIELD}
        value={p.newProjectTitle}
        onInput={(e) =>
          p.setNewProjectTitle((e.target as HTMLInputElement).value)
        }
        placeholder={t("My project")}
        autoFocus
      />
    </div>
  );
}

// FolderNameField is the folder the project gets in the workspace.
function FolderNameField() {
  const p = useApp().projects;
  return (
    <div>
      <label class={LABEL}>{t("Folder name")}</label>
      <input
        class={MONO_FIELD}
        value={p.newProjectDir}
        onInput={(e) =>
          p.setNewProjectDir((e.target as HTMLInputElement).value)
        }
        placeholder={slugOf(p.newProjectTitle) || "my-project"}
      />
      <p class="text-xs text-[#7a7a8c] mt-1">
        {t(
          "Made from the name when empty. A folder under the workspace, a simple name with no paths.",
        )}
      </p>
    </div>
  );
}

// FolderFields is where the project lives: a folder in the workspace, or, when Settings → Security
// allows the whole disk, any folder found with the folder browser.
function FolderFields() {
  const p = useApp().projects;
  if (!p.fullDisk) {
    return <FolderNameField />;
  }
  return (
    <div data-testid="new-project-location">
      <label class={LABEL}>{t("Location")}</label>
      {p.newProjectPath && (
        <p
          class="px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 font-mono text-xs text-[#e8e8ea] break-all"
          data-testid="new-project-path"
        >
          {p.newProjectPath}
        </p>
      )}
      {!p.newProjectPath && <FolderNameField />}
      <div class="flex gap-2 mt-2">
        <button
          type="button"
          class="px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5"
          data-testid="new-project-browse"
          onClick={() => p.openFolderPicker("new")}
        >
          {t("Choose a folder…")}
        </button>
        {p.newProjectPath && (
          <button
            type="button"
            class="px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#9a9aaa] hover:bg-white/5"
            data-testid="new-project-path-clear"
            onClick={() => p.setNewProjectPath("")}
          >
            {t("Use the workspace")}
          </button>
        )}
      </div>
      <p class="text-xs text-[#7a7a8c] mt-1">
        {t(
          "The whole disk is allowed in Settings → Security, so the project can live in any folder. The folder is used as it is and is never deleted with the project.",
        )}
      </p>
    </div>
  );
}

// MoreOptions is everything with a sensible default, behind one line.
function MoreOptions() {
  const p = useApp().projects;
  return (
    <details
      class="group rounded-xl border border-white/10 bg-black/15"
      data-testid="new-project-more"
    >
      <summary class="cursor-pointer select-none px-3 py-2.5 text-sm text-[#9a9aaa] hover:text-[#e8e8ea] list-none flex items-center gap-2">
        <svg
          class="transition-transform group-open:rotate-90"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
        {t("More options")}
      </summary>
      <div class="px-3 pb-3 space-y-4">
        <div>
          <label class={LABEL}>{t("Description (optional)")}</label>
          <input
            class={FIELD}
            value={p.newProjectDesc}
            onInput={(e) =>
              p.setNewProjectDesc((e.target as HTMLInputElement).value)
            }
            placeholder={t("What this project is about")}
          />
        </div>
        <FolderFields />
        {p.hasPodman && <PodmanOffer />}
      </div>
    </details>
  );
}

// CreateButtons are the create and cancel buttons, and what is happening once create was pressed.
function CreateButtons() {
  const p = useApp().projects;
  return (
    <>
      {p.creatingProject && (
        <div class="mt-4 flex items-center gap-2 text-sm text-[#9a9aaa]">
          <IconSpinner class="animate-spin flex-none text-accent" size={16} />
          <span>{t("Creating the project…")}</span>
        </div>
      )}
      <div class="flex gap-2 mt-5">
        <button
          class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
          onClick={() => p.createProject()}
          disabled={p.creatingProject || !p.newProjectTitle.trim()}
        >
          {p.creatingProject && (
            <IconSpinner
              class="animate-spin"
              size={16}
              style="display:inline-block;vertical-align:middle;margin-right:6px"
            />
          )}
          {choose(p.creatingProject, t("Creating…"), t("Create"))}
        </button>
        <button
          class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
          onClick={p.closeNewProject}
        >
          {t("Cancel")}
        </button>
      </div>
    </>
  );
}

export function NewProjectModal() {
  const { closeNewProject } = useApp().projects;
  return (
    <ModalBackdrop onClose={closeNewProject}>
      <dialog
        open
        class="app-dialog frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl max-h-[92vh] overflow-y-auto"
        aria-label={t(NEW_PROJECT)}
      >
        <div class="flex items-center gap-2 mb-4">
          <IconFolder size={20} class="text-accent" />
          <h2 class="text-base font-semibold">{t(NEW_PROJECT)}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeNewProject}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="space-y-4">
          <NameField />
          <MoreOptions />
        </div>
        <CreateButtons />
      </dialog>
    </ModalBackdrop>
  );
}
