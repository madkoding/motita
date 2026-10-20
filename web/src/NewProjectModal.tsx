// The new project modal. A project is a plain folder, so it asks for its name first and then which
// folder. The folder browser picks an existing one or makes a new one; choosing one names a project
// that has no name yet after it. The rest is folded away.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { folderNameOf } from "./appParse";
import { podmanNote } from "./appFormat";
import { IconClose, IconFolder, IconSpinner } from "./icons";
import { NEW_PROJECT } from "./appConst";
import { useFolderProbe, type FolderProbe } from "./useFolderProbe";

// The look of the dialog's fields.
const FIELD =
  "w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";

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

// NameField is the project's name. Left empty it is the folder's name, which is what the
// placeholder shows.
function NameField() {
  const p = useApp().projects;
  return (
    <div>
      <label class={LABEL}>{t("Name")}</label>
      <input
        class={FIELD}
        data-testid="new-project-name"
        value={p.newProjectTitle}
        onInput={(e) =>
          p.setNewProjectTitle((e.target as HTMLInputElement).value)
        }
        placeholder={folderNameOf(p.newProjectPath) || t("My project")}
      />
      {!p.newProjectPath && (
        <p class="text-xs text-[#7a7a8c] mt-1">
          {t("Its folder is created in the workspace with this name.")}
        </p>
      )}
    </div>
  );
}

// FolderNote says what will happen to the folder typed: that it is created, that it is used as it
// is and stays when the project goes, or that it is among the system's files and needs a yes.
function FolderNote({ probe }: { probe: FolderProbe }) {
  const p = useApp().projects;
  if (probe.state === "idle") {
    return null;
  }
  if (probe.state === "refused") {
    return (
      <p class="text-xs text-danger mt-1" data-testid="folder-note-refused">
        {probe.reason}
      </p>
    );
  }
  if (probe.state === "missing") {
    return (
      <p class="text-xs text-[#9a9aaa] mt-1" data-testid="folder-note-missing">
        {t("The folder does not exist yet: it will be created.")}
      </p>
    );
  }
  if (probe.state === "exists") {
    return (
      <p class="text-xs text-[#9a9aaa] mt-1" data-testid="folder-note-exists">
        {t(
          "The folder already exists and is used as it is. Deleting the project asks whether it stays on disk.",
        )}
      </p>
    );
  }
  return (
    <label
      class="flex items-start gap-2 mt-1 text-xs text-warning"
      data-testid="folder-note-sensitive"
    >
      <input
        type="checkbox"
        class="mt-0.5"
        data-testid="folder-sure"
        checked={p.newProjectSure}
        onChange={(e) =>
          p.setNewProjectSure((e.target as HTMLInputElement).checked)
        }
      />
      <span>
        {t(
          "This folder is among the system's own files. I know what I am doing and want a project here.",
        )}
      </span>
    </label>
  );
}

// FolderField is where the project lives: the folder chosen (or made) in the folder browser, or a
// new folder in the workspace while none is chosen.
function FolderField({ probe }: { probe: FolderProbe }) {
  const p = useApp().projects;
  return (
    <div data-testid="new-project-location">
      <label class={LABEL}>{t("Folder")}</label>
      <div class="flex items-center gap-2">
        <p
          class="flex-1 min-w-0 flex items-center gap-2 px-3 min-h-[44px] rounded-xl bg-black/30 border border-white/10 text-sm text-[#e8e8ea]"
          data-testid="new-project-folder"
        >
          <IconFolder size={16} class="flex-none text-accent" />
          <span
            class={`min-w-0 break-all${choose(p.newProjectPath, " font-mono text-xs", " text-[#9a9aaa]")}`}
          >
            {p.newProjectPath || t("A new folder in the workspace")}
          </span>
        </p>
        <button
          type="button"
          class="flex-none px-3 min-h-[44px] rounded-xl border border-white/10 text-sm text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform"
          data-testid="new-project-browse"
          onClick={() => p.openFolderPicker("new")}
        >
          {choose(p.newProjectPath, t("Change…"), t("Choose or create…"))}
        </button>
      </div>
      <FolderNote probe={probe} />
      {p.newProjectPath && (
        <button
          type="button"
          class="mt-1.5 text-xs text-[#9a9aaa] underline hover:text-[#e8e8ea]"
          data-testid="new-project-path-clear"
          onClick={() => p.setNewProjectPath("")}
        >
          {t("Use the workspace")}
        </button>
      )}
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
          disabled={
            p.creatingProject ||
            !(p.newProjectTitle.trim() || folderNameOf(p.newProjectPath))
          }
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
  const { projects, api } = useApp();
  const { closeNewProject } = projects;
  const probe = useFolderProbe(api, projects.newProjectPath);
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
          <FolderField probe={probe} />
          <MoreOptions />
        </div>
        <CreateButtons />
      </dialog>
    </ModalBackdrop>
  );
}
