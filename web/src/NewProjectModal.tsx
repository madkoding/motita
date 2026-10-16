// The new project modal: first the one question that decides everything else (empty, or from a
// repository), then the form for that choice.
import { useCallback } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { repoNameOf, slugOf } from "./appParse";
import { podmanNote } from "./appFormat";
import { connectedAccounts } from "./gitApi";
import { IconClose, IconFolder, IconFolderPlus, IconSpinner } from "./icons";
import { NEW_PROJECT } from "./appConst";
import { ReposSection } from "./ReposField";
import type { MainRepo } from "./projectRepos";

// The look of the dialog's fields.
const FIELD =
  "w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";
const MONO_FIELD = FIELD + " font-mono text-sm";
const CHOICE =
  "w-full flex items-center gap-3 p-3.5 rounded-xl border border-white/10 bg-black/20 hover:border-accent/50 hover:bg-white/5 text-left active:scale-[0.99] transition";

// headingOf is the dialog's title for the screen it is on.
function headingOf(mode: string | null): string {
  if (mode === "empty") {
    return t("Empty project");
  }
  if (mode === "multi") {
    return t("Several repositories");
  }
  return t(choose(mode === "git", "From a Git repository", NEW_PROJECT));
}

// ChooseStart is the first screen: the one question that decides everything else.
function ChooseStart() {
  const { setNewProjectMode } = useApp().projects;
  return (
    <div class="space-y-2.5" data-testid="new-project-choose">
      <p class="text-sm text-[#9a9aaa]">{t("How do you want to start?")}</p>
      <button
        type="button"
        class={CHOICE}
        data-testid="new-project-empty"
        onClick={() => setNewProjectMode("empty")}
      >
        <IconFolderPlus size={22} class="flex-none text-accent" hidden />
        <span>
          <span class="block text-sm font-semibold text-[#e8e8ea]">
            {t("Empty project")}
          </span>
          <span class="block text-xs text-[#7a7a8c] mt-0.5">
            {t("A new folder to start from scratch")}
          </span>
        </span>
      </button>
      <button
        type="button"
        class={CHOICE}
        data-testid="new-project-git"
        onClick={() => setNewProjectMode("git")}
      >
        <svg
          class="flex-none text-accent"
          width="22"
          height="22"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="6" cy="6" r="2.5" />
          <circle cx="6" cy="18" r="2.5" />
          <circle cx="18" cy="9" r="2.5" />
          <path d="M6 8.5v7" />
          <path d="M18 11.5c0 4-12 1-12 4" />
        </svg>
        <span>
          <span class="block text-sm font-semibold text-[#e8e8ea]">
            {t("From a Git repository")}
          </span>
          <span class="block text-xs text-[#7a7a8c] mt-0.5">
            {t("Clone a repository from GitHub, GitLab or any URL")}
          </span>
        </span>
      </button>
      <button
        type="button"
        class={CHOICE}
        data-testid="new-project-multi"
        onClick={() => setNewProjectMode("multi")}
      >
        <IconFolder size={22} class="flex-none text-accent" />
        <span>
          <span class="block text-sm font-semibold text-[#e8e8ea]">
            {t("Several repositories")}
          </span>
          <span class="block text-xs text-[#7a7a8c] mt-0.5">
            {t("A folder that holds all the repositories you clone into it")}
          </span>
        </span>
      </button>
    </div>
  );
}

// RepoAccounts offers the person's own repositories when a git host is connected, and connecting
// one when none is.
function RepoAccounts() {
  const { gitAccounts, openRepoPicker, setGitConnect } = useApp().projects;
  if (!gitAccounts) {
    return null;
  }
  if (connectedAccounts(gitAccounts).length > 0) {
    return (
      <button
        type="button"
        class="mt-2 text-sm text-accent underline underline-offset-2"
        onClick={() => openRepoPicker()}
      >
        {t("Choose from my repositories")}
      </button>
    );
  }
  return (
    <div
      class="mt-2 flex items-center gap-2 p-2.5 rounded-xl border border-white/10 bg-black/20"
      data-testid="git-connect-banner"
    >
      <span class="flex-1 text-xs text-[#9a9aaa]">
        {t(
          "Connect GitHub, GitLab or Bitbucket to pick one of your repositories",
        )}
      </span>
      <button
        type="button"
        class="flex-none px-3 min-h-[32px] rounded-lg bg-accent text-white text-xs font-semibold"
        onClick={() => setGitConnect({ after: "picker" })}
      >
        {t("Connect")}
      </button>
    </div>
  );
}

// RepoField is the repository URL: it names the project and the folder, so it comes before them.
function RepoField() {
  const { newProjectGit, setNewProjectGit } = useApp().projects;
  return (
    <div>
      <label class={LABEL}>
        {t("Repository URL")} <span class="text-danger">*</span>
      </label>
      <input
        class={MONO_FIELD}
        value={newProjectGit}
        onInput={(e) => setNewProjectGit((e.target as HTMLInputElement).value)}
        placeholder={t("https://github.com/user/repo.git")}
        autoFocus
      />
      <RepoAccounts />
    </div>
  );
}

// MainBranchField is the branch the project goes back to when it has no sessions.
function MainBranchField() {
  const { newProjectMain, chooseMain, remoteMain } = useApp().projects;
  const branches = Array.from(
    new Set([
      "main",
      "master",
      newProjectMain,
      ...choose(remoteMain, [remoteMain], []),
    ]),
  );
  return (
    <div>
      <label class={LABEL} for="new-project-main">
        {t("Main branch")}
      </label>
      <select
        id="new-project-main"
        data-testid="new-project-main"
        class={MONO_FIELD}
        value={newProjectMain}
        onChange={(e) => chooseMain((e.target as HTMLSelectElement).value)}
      >
        {branches.map((b) => (
          <option key={b} value={b}>
            {b}
          </option>
        ))}
      </select>
      <p class="text-xs text-[#7a7a8c] mt-1">
        {choose(
          remoteMain,
          tf("The repository uses {branch}.", { branch: remoteMain }) + " ",
          "",
        )}
        {t("The project returns to this branch when it has no sessions.")}
      </p>
    </div>
  );
}

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

// folderPlaceholder is the folder the project gets when the field is left empty.
function folderPlaceholder(
  mode: string | null,
  git: string,
  title: string,
): string {
  const name = choose(mode === "git", repoNameOf(git) || title, title);
  return slugOf(name) || "my-project";
}

// newMainRepo is the new folder of an empty project; a repository is already shown by its field.
function newMainRepo(
  mode: string | null,
  title: string,
  dir: string,
): MainRepo | null {
  if (mode === "git" || mode === "multi") {
    return null;
  }
  return {
    name: dir.trim() || folderPlaceholder(mode, "", title),
    source: t("A new folder"),
  };
}

// NewProjectRepos lists the repositories the project will have: the main one, and any others.
function NewProjectRepos() {
  const p = useApp().projects;
  const { removeNewProjectRepo } = p;
  const connected =
    !!p.gitAccounts && connectedAccounts(p.gitAccounts).length > 0;
  const onRemove = useCallback(
    (name: string) => removeNewProjectRepo(name),
    [removeNewProjectRepo],
  );
  return (
    <ReposSection
      main={newMainRepo(p.newProjectMode, p.newProjectTitle, p.newProjectDir)}
      repos={p.newProjectRepos}
      busy=""
      onRemove={onRemove}
      onAdd={p.addNewProjectRepo}
      onChoose={choose(connected, p.openOtherRepoPicker, undefined)}
      catalog={p.repoCatalog}
      title={choose(
        p.newProjectMode === "git",
        "Other repositories",
        "Repositories",
      )}
      hint={t(
        "Each one is cloned once and shared by your projects. Every session gets its own copy.",
      )}
    />
  );
}

// NameField is the project's name: required for a new folder, optional (from the URL) for a repository.
function NameField({ git }: { git: boolean }) {
  const p = useApp().projects;
  return (
    <div>
      <label class={LABEL}>
        {t("Name")}
        {!git && (
          <>
            {" "}
            <span class="text-danger">*</span>
          </>
        )}
      </label>
      <input
        class={FIELD}
        value={p.newProjectTitle}
        onInput={(e) =>
          p.setNewProjectTitle((e.target as HTMLInputElement).value)
        }
        placeholder={choose(
          git,
          repoNameOf(p.newProjectGit) || t("The repository name"),
          t("My project"),
        )}
        autoFocus={!git}
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
        placeholder={folderPlaceholder(
          p.newProjectMode,
          p.newProjectGit,
          p.newProjectTitle,
        )}
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
      {p.newProjectPath ? (
        <p
          class="px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 font-mono text-xs text-[#e8e8ea] break-all"
          data-testid="new-project-path"
        >
          {p.newProjectPath}
        </p>
      ) : (
        <FolderNameField />
      )}
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
        {p.newProjectMode === "git" && <NameField git />}
        <MainBranchField />
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

// createLabel is the create button's text, by what it does and whether it is doing it.
function createLabel(creating: boolean, git: boolean): string {
  if (creating) {
    return t(choose(git, "Cloning…", "Creating…"));
  }
  return t(choose(git, "Clone and create", "Create"));
}

// CreateButtons are the create and cancel buttons, and what is happening once create was pressed.
function CreateButtons() {
  const p = useApp().projects;
  const git = p.newProjectMode === "git";
  const missing = choose(
    git,
    !p.newProjectGit.trim(),
    !p.newProjectTitle.trim() ||
      (p.newProjectMode === "multi" && p.newProjectRepos.length === 0),
  );
  return (
    <>
      {p.creatingProject && (
        <div class="mt-4 flex items-center gap-2 text-sm text-[#9a9aaa]">
          <IconSpinner class="animate-spin flex-none text-accent" size={16} />
          <span>
            {choose(git, t("Cloning repository…"), t("Creating the project…"))}
          </span>
        </div>
      )}
      <div class="flex gap-2 mt-5">
        <button
          class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
          onClick={() => p.createProject()}
          disabled={p.creatingProject || missing}
        >
          {p.creatingProject && (
            <IconSpinner
              class="animate-spin"
              size={16}
              style="display:inline-block;vertical-align:middle;margin-right:6px"
            />
          )}
          {createLabel(p.creatingProject, git)}
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

// ProjectForm is the form for the start that was chosen.
function ProjectForm() {
  const p = useApp().projects;
  const git = p.newProjectMode === "git";
  return (
    <>
      <div class="space-y-4">
        {git && <RepoField />}
        {!git && <NameField git={false} />}
        <NewProjectRepos />
        <MoreOptions />
      </div>
      <CreateButtons />
    </>
  );
}

// NewProjectHead is the dialog's title row: back to the first screen, the title, and close.
function NewProjectHead() {
  const {
    newProjectMode,
    creatingProject,
    setNewProjectMode,
    closeNewProject,
  } = useApp().projects;
  const canGoBack = !!newProjectMode && !creatingProject;
  return (
    <div class="flex items-center gap-2 mb-4">
      {canGoBack && (
        <button
          class="p-1.5 -ml-1.5 rounded-lg hover:bg-white/5"
          onClick={() => setNewProjectMode(null)}
          aria-label={t("Back")}
          data-testid="new-project-back"
        >
          <svg
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <polyline points="15 18 9 12 15 6" />
          </svg>
        </button>
      )}
      {!canGoBack && <IconFolder size={20} class="text-accent" />}
      <h2 class="text-base font-semibold">{headingOf(newProjectMode)}</h2>
      <button
        class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
        onClick={closeNewProject}
        aria-label={t("Close")}
      >
        <IconClose size={18} />
      </button>
    </div>
  );
}

export function NewProjectModal() {
  const { newProjectMode, closeNewProject } = useApp().projects;
  return (
    <ModalBackdrop onClose={closeNewProject}>
      <dialog
        open
        class="app-dialog frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl max-h-[92vh] overflow-y-auto"
        aria-label={t(NEW_PROJECT)}
      >
        <NewProjectHead />
        {!newProjectMode && <ChooseStart />}
        {newProjectMode && <ProjectForm />}
      </dialog>
    </ModalBackdrop>
  );
}
