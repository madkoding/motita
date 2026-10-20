// Projects: the New project dialog (a name and a folder), the edit dialog, podman, and deleting one.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { folderNameOf, slugOf } from "./appParse";
import { useFolderPicker } from "./useFolderPicker";
import { createdToast } from "./projectToast";
import { SCAN_STARTED_EVENT } from "./notesApi";
import type { SessionList } from "./useSessionList";
import type { Api } from "./prApi";
import type {
  EditProjectDraft,
  GitConnectRequest,
  ProjectInfo,
  SetStatus,
  SetToast,
} from "./appTypes";
import {
  CREATE_SESSION_EVENT,
  HTTP_NO_CONTENT,
  JSON_HEADERS,
  NEW_PROJECT_SESSION_DELAY_MS,
  PROJECTS_PATH,
} from "./appConst";

// draftOf is what the edit dialog starts from: the project's own values.
function draftOf(p: ProjectInfo): EditProjectDraft {
  return {
    id: p.id,
    title: p.title,
    description: p.description ?? "",
    main: p.main_branch ?? "main",
    branches: [p.main_branch || "main"],
    current: p.branch ?? "",
    mergeMethod: p.merge_method ?? "",
    prMaxFixes: p.pr_max_fixes ?? 0,
    autoMerge: p.auto_merge === true,
    autoContinue: p.auto_continue === true,
    dir: p.dir,
    path: "",
  };
}

// withBranches puts what the gateway says about a project's branches into the edit dialog.
function withBranches(
  prev: EditProjectDraft,
  d: { branches?: string[]; main_branch?: string; current?: string },
): EditProjectDraft {
  return {
    ...prev,
    branches: choose(d.branches?.length, d.branches as string[], prev.branches),
    main: d.main_branch || prev.main,
    current: d.current ?? prev.current,
  };
}

// openSessionIn asks the page to open a session in a project that was just created, so the user
// lands in the chat.
function openSessionIn(projectId: string) {
  setTimeout(() => {
    const evt = new CustomEvent(CREATE_SESSION_EVENT, {
      detail: { project_id: projectId },
    });
    window.dispatchEvent(evt);
  }, NEW_PROJECT_SESSION_DELAY_MS);
}

// useEditProject is the edit-project dialog: what is being edited, the branches its selector
// offers, and saving it.
function useEditProject(
  api: Api,
  fetchProjects: () => Promise<unknown>,
  setToast: SetToast,
) {
  const [editProject, setEditProject] = useState<EditProjectDraft | null>(null);
  const [savingProject, setSavingProject] = useState(false);

  // openEditProject opens the edit dialog with the project's own values, and asks the gateway
  // which branches the main-branch selector can offer.
  const openEditProject = useCallback(async (p: ProjectInfo) => {
    setEditProject(draftOf(p));
    try {
      const res = await api(
        PROJECTS_PATH + encodeURIComponent(p.id) + "/branches",
      );
      if (!res.ok) {
        return;
      }
      const d = await res.json();
      setEditProject((prev) => {
        if (prev?.id !== p.id) {
          return prev;
        }
        return withBranches(prev, d);
      });
    } catch {
      /* the selector keeps the branch it already knows */
    }
  }, []);

  // saveEditProject sends what the dialog holds. The gateway puts the project back on its main
  // branch when it has no sessions, so the list is read again afterwards.
  const saveEditProject = useCallback(async () => {
    if (!editProject?.title.trim()) {
      return;
    }
    setSavingProject(true);
    try {
      const res = await api(
        PROJECTS_PATH + encodeURIComponent(editProject.id),
        {
          method: "PATCH",
          headers: JSON_HEADERS,
          body: JSON.stringify({
            title: editProject.title,
            description: editProject.description,
            main_branch: editProject.main,
            merge_method: editProject.mergeMethod,
            pr_max_fixes: editProject.prMaxFixes,
            auto_merge: editProject.autoMerge,
            auto_continue: editProject.autoContinue,
            path: editProject.path || undefined,
          }),
        },
      );
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        setToast({
          message: t("Could not save the project"),
          type: "error",
          detail: err.error,
        });
        return;
      }
      setEditProject(null);
      await fetchProjects();
      setToast({ message: t("Project saved"), type: "success" });
    } catch {
      setToast({ message: t("Could not save the project"), type: "error" });
    } finally {
      setSavingProject(false);
    }
  }, [editProject, fetchProjects]);

  // patchEditProject changes one field of the edit dialog.
  const patchEditProject = useCallback((patch: Partial<EditProjectDraft>) => {
    setEditProject((p) => p && { ...p, ...patch });
  }, []);
  const closeEditProject = useCallback(() => setEditProject(null), []);
  return {
    editProject,
    patchEditProject,
    closeEditProject,
    savingProject,
    openEditProject,
    saveEditProject,
  };
}

interface Deps {
  api: Api;
  lists: SessionList;
  setState: SetStatus;
  setToast: SetToast;
}

// useNewProjectForm holds what the New project dialog has been given so far, and empties it.
function useNewProjectForm() {
  const [showNewProject, setShowNewProject] = useState(false);
  const [newProjectTitle, setNewProjectTitle] = useState("");
  const [newProjectDesc, setNewProjectDesc] = useState("");
  const [newProjectDir, setNewProjectDir] = useState("");
  // A folder outside the workspace picked in the folder browser; "" is a folder in the workspace.
  const [newProjectPath, setNewProjectPath] = useState("");
  // The person confirmed that the folder is among the system's own files.
  const [newProjectSure, setNewProjectSure] = useState(false);
  // The user's answer to podman.
  const [newProjectPodman, setNewProjectPodman] = useState(false);
  const [creatingProject, setCreatingProject] = useState(false);

  // resetNewProject empties the New project dialog once the project exists.
  const resetNewProject = () => {
    setShowNewProject(false);
    setNewProjectTitle("");
    setNewProjectDesc("");
    setNewProjectDir("");
    setNewProjectPath("");
    setNewProjectSure(false);
    setNewProjectPodman(false);
    setCreatingProject(false);
  };

  // openNewProject opens the New project dialog: a project is a plain folder, so it starts empty.
  const openNewProject = () => {
    setShowNewProject(true);
  };
  const closeNewProject = useCallback(() => setShowNewProject(false), []);

  return {
    showNewProject,
    openNewProject,
    closeNewProject,
    newProjectTitle,
    setNewProjectTitle,
    newProjectDesc,
    setNewProjectDesc,
    newProjectDir,
    setNewProjectDir,
    newProjectPath,
    setNewProjectPath,
    newProjectSure,
    setNewProjectSure,
    newProjectPodman,
    setNewProjectPodman,
    creatingProject,
    setCreatingProject,
    resetNewProject,
  };
}

type NewProjectForm = ReturnType<typeof useNewProjectForm>;

// useGitHosts is the git host connect modal, opened from Settings or from a pull request that needs
// a host, and the counter that tells Settings a host was connected.
function useGitHosts(setToast: SetToast) {
  const [gitConnect, setGitConnect] = useState<GitConnectRequest | null>(null);
  const [gitRev, setGitRev] = useState(0);
  const onGitConnected = useCallback((_service: string, account: string) => {
    setGitRev((n) => n + 1);
    setToast({
      message: choose(
        account,
        tf("Connected as {user}", { user: account }),
        t("Connected"),
      ),
      type: "success",
    });
  }, []);
  const closeGitConnect = useCallback(() => setGitConnect(null), []);
  return { gitConnect, setGitConnect, closeGitConnect, onGitConnected, gitRev };
}

// useRuntimes asks the gateway which container runtimes the machine has, so podman is only
// offered when it is there. Read on start (the project menu needs it) and again when the dialog
// opens.
function useRuntimes(api: Api, showNewProject: boolean) {
  const [hasPodman, setHasPodman] = useState(false);
  const [hasPodmanCompose, setHasPodmanCompose] = useState(false);
  // Installed is not the same as working: `podman info` can fail on a machine that has the binary.
  const [podmanReady, setPodmanReady] = useState(false);
  useEffect(() => {
    let live = true;
    const set = (d: {
      podman?: boolean;
      podman_compose?: boolean;
      podman_ready?: boolean;
    }) => {
      setHasPodman(!!d.podman);
      setHasPodmanCompose(!!d.podman_compose);
      setPodmanReady(!!d.podman_ready);
    };
    api("/v1/runtimes")
      .then((r) => r.json())
      .then((d) => {
        if (live) {
          set(d);
        }
      })
      .catch(() => {
        if (live) {
          set({});
        }
      });
    return () => {
      live = false;
    };
  }, [showNewProject]);
  return { hasPodman, hasPodmanCompose, podmanReady };
}

// newProjectTarget is the title and folder a new project is created with: the title defaults to the
// name of the folder chosen, and the folder (in the workspace) to the title's slug.
function newProjectTarget(form: NewProjectForm) {
  const title =
    form.newProjectTitle.trim() || folderNameOf(form.newProjectPath);
  const dir = form.newProjectDir.trim() || slugOf(title);
  return { title, dir };
}

// useCreateProject sends the New project dialog to the gateway, and handles its answer.
function useCreateProject(
  api: Api,
  form: NewProjectForm,
  hasPodman: boolean,
  fetchProjects: () => Promise<unknown>,
  setState: SetStatus,
  setToast: SetToast,
) {
  // createProject sends a new project to the gateway.
  const createProject = useCallback(async () => {
    const { title, dir } = newProjectTarget(form);
    if (!title || (!dir && !form.newProjectPath)) {
      return;
    }
    form.setCreatingProject(true);
    try {
      const res = await api("/v1/projects", {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({
          title,
          description: form.newProjectDesc.trim(),
          dir,
          path: form.newProjectPath || undefined,
          confirm_sensitive: form.newProjectSure || undefined,
          use_podman: choose(
            hasPodman && form.newProjectPodman,
            true,
            undefined,
          ),
        }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        form.setCreatingProject(false);
        setState(err.error || "could not create the project", true);
        return;
      }
      const data = await res.json();
      await fetchProjects();
      // Auto-create a session in the new project so the user lands in the chat.
      if (data.id) {
        openSessionIn(data.id);
      }
      // Show a toast and close the modal.
      setToast(createdToast(title));
      form.resetNewProject();
    } catch (e) {
      setState(
        tf("could not create the project: {err}", { err: String(e) }),
        true,
      );
      form.setCreatingProject(false);
    }
  }, [
    form.newProjectTitle,
    form.newProjectDesc,
    form.newProjectDir,
    form.newProjectPath,
    form.newProjectSure,
    form.newProjectPodman,
    hasPodman,
    fetchProjects,
  ]);
  return createProject;
}

// useProjectAdmin changes and deletes projects that already exist.
function useProjectAdmin(
  api: Api,
  lists: SessionList,
  setState: SetStatus,
  setToast: SetToast,
) {
  const { fetchProjects, fetchSessions } = lists;

  // setProjectPodman changes the answer given at creation: the gateway tells the open sessions.
  const setProjectPodman = useCallback(
    async (id: string, podman: boolean) => {
      try {
        const res = await api(PROJECTS_PATH + id, {
          method: "PATCH",
          headers: JSON_HEADERS,
          body: JSON.stringify({ podman }),
        });
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || "could not change the project", true);
          return;
        }
        await fetchProjects();
      } catch {
        setState("could not change the project", true);
      }
    },
    [fetchProjects],
  );

  // refreshProjectMain asks the gateway to bring the project's main branch up to date now, the
  // only way it moves since nothing updates it by itself. The notice that remains comes with the list.
  const refreshProjectMain = useCallback(
    async (id: string) => {
      try {
        const res = await api(PROJECTS_PATH + id + "/main/refresh", {
          method: "POST",
        });
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || "could not update the main branch", true);
          return;
        }
        await fetchProjects();
      } catch {
        setState("could not update the main branch", true);
      }
    },
    [fetchProjects],
  );

  // rescanNotes writes the notes of one project again in the background, with the model options
  // of the last scan; the sidebar shows how far it got.
  const rescanNotes = useCallback(
    async (id: string) => {
      try {
        const res = await api(
          "/v1/notes/scan?again=1&project=" + encodeURIComponent(id),
          { method: "POST" },
        );
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || "could not rescan the notes", true);
          return;
        }
        setToast({
          message: t(
            "Rescanning the notes of this project in the background. The sidebar shows how far it got.",
          ),
          type: "success",
        });
        window.dispatchEvent(new Event(SCAN_STARTED_EVENT));
      } catch {
        setState("could not rescan the notes", true);
      }
    },
    [api, setState, setToast],
  );

  // deleteProject removes a project from the gateway.
  // `discard` carries the user's answer, exactly as it does for a session: a project
  // whose sessions hold uncommitted work is refused without it. `wipe` asks to remove the
  // project's folder with its contents too; without it the folder stays on disk.
  const deleteProject = useCallback(
    async (id: string, discard = false, wipe = false) => {
      try {
        const query = [
          choose(discard, "force=1", ""),
          choose(wipe, "folder=delete", ""),
        ].filter(Boolean);
        const res = await api(
          PROJECTS_PATH +
            id +
            choose(query.length > 0, "?" + query.join("&"), ""),
          { method: "DELETE" },
        );
        if (!res.ok && res.status !== HTTP_NO_CONTENT) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || "could not delete the project", true);
          return;
        }
        await fetchProjects();
        await fetchSessions();
      } catch {
        /* ignore */
      }
    },
    [fetchProjects, fetchSessions],
  );

  return { setProjectPodman, refreshProjectMain, rescanNotes, deleteProject };
}

export function useProjects({ api, lists, setState, setToast }: Deps) {
  const { fetchProjects } = lists;
  const edit = useEditProject(api, fetchProjects, setToast);
  const form = useNewProjectForm();
  const git = useGitHosts(setToast);
  const runtimes = useRuntimes(api, form.showNewProject);
  const createProject = useCreateProject(
    api,
    form,
    runtimes.hasPodman,
    fetchProjects,
    setState,
    setToast,
  );
  const admin = useProjectAdmin(api, lists, setState, setToast);
  const folders = useFolderPicker(
    api,
    form.showNewProject || !!edit.editProject,
    form.setNewProjectPath,
    edit.patchEditProject,
  );

  return {
    showNewProject: form.showNewProject,
    openNewProject: form.openNewProject,
    closeNewProject: form.closeNewProject,
    newProjectTitle: form.newProjectTitle,
    setNewProjectTitle: form.setNewProjectTitle,
    newProjectDesc: form.newProjectDesc,
    setNewProjectDesc: form.setNewProjectDesc,
    newProjectDir: form.newProjectDir,
    setNewProjectDir: form.setNewProjectDir,
    newProjectPath: form.newProjectPath,
    setNewProjectPath: form.setNewProjectPath,
    newProjectSure: form.newProjectSure,
    setNewProjectSure: form.setNewProjectSure,
    ...folders,
    ...edit,
    ...git,
    ...runtimes,
    newProjectPodman: form.newProjectPodman,
    setNewProjectPodman: form.setNewProjectPodman,
    setProjectPodman: admin.setProjectPodman,
    refreshProjectMain: admin.refreshProjectMain,
    rescanNotes: admin.rescanNotes,
    creatingProject: form.creatingProject,
    createProject,
    deleteProject: admin.deleteProject,
  };
}

export type Projects = ReturnType<typeof useProjects>;
