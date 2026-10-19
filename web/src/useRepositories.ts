// Repositories: the list the sidebar shows, the dialog that records one more (cloned from a URL, or
// a folder the machine already has) and deleting one. A session works on a repository through the
// project id `repo_<id>` (see repoScope), the way it works on a project.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { errorMessage } from "./errors";
import type { Api } from "./prApi";
import type { ProjectInfo, RepositoryInfo, SetToast } from "./appTypes";
import { JSON_HEADERS } from "./appConst";

const REPOS_PATH = "/v1/repos";

// repoScope is the project id a session of repository id carries.
export function repoScope(id: string): string {
  return "repo_" + id;
}

// reasonOf is the gateway's reason for a refused request, or the fallback.
async function reasonOf(res: Response, fallback: string): Promise<string> {
  const body = await res.json().catch(() => ({}));
  return body.error || fallback;
}

// projectsKey changes when a project is added, removed or gets another repository: the records of
// a project's repositories are made when the list is read, so it is read again then.
function projectsKey(projects: ProjectInfo[]): string {
  return projects.map((p) => p.id + ":" + (p.repos?.length ?? 0)).join(",");
}

export function useRepositories({
  api,
  setToast,
  projects,
}: {
  api: Api;
  setToast: SetToast;
  projects: ProjectInfo[];
}) {
  const [repositories, setRepositories] = useState<RepositoryInfo[]>([]);
  const [showNewRepository, setShowNewRepository] = useState(false);
  const [newRepoUrl, setNewRepoUrl] = useState("");
  const [newRepoPath, setNewRepoPath] = useState("");
  const [creatingRepository, setCreatingRepository] = useState(false);
  const [newRepoError, setNewRepoError] = useState("");

  // fetchRepositories reads the list again. A gateway that cannot answer leaves the last list.
  const fetchRepositories = useCallback(async () => {
    try {
      const res = await api(REPOS_PATH);
      const d: { repositories?: RepositoryInfo[] } = await res.json();
      setRepositories(d.repositories ?? []);
    } catch {
      /* the list stays as it was */
    }
  }, [api]);

  const key = projectsKey(projects);
  useEffect(() => {
    void fetchRepositories();
  }, [fetchRepositories, key]);

  const openNewRepository = useCallback(() => {
    setNewRepoError("");
    setShowNewRepository(true);
  }, []);
  const closeNewRepository = useCallback(() => {
    setShowNewRepository(false);
    setNewRepoUrl("");
    setNewRepoPath("");
    setNewRepoError("");
  }, []);

  // createRepository records the repository the dialog describes: the gateway clones the URL, or
  // takes the folder where it is. A refusal stays in the dialog, with the gateway's reason.
  const createRepository = useCallback(async () => {
    setCreatingRepository(true);
    setNewRepoError("");
    try {
      const res = await api(REPOS_PATH, {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({
          git_url: newRepoUrl.trim(),
          path: newRepoPath.trim(),
        }),
      });
      if (!res.ok) {
        setNewRepoError(await reasonOf(res, t("Could not add the repository")));
        return;
      }
      closeNewRepository();
      await fetchRepositories();
    } catch (e) {
      setNewRepoError(errorMessage(e));
    } finally {
      setCreatingRepository(false);
    }
  }, [api, newRepoUrl, newRepoPath, closeNewRepository, fetchRepositories]);

  // deleteRepository forgets a repository no project or session uses; a refusal (work that
  // exists nowhere else, a project still using it) is told as it is.
  const deleteRepository = useCallback(
    async (r: RepositoryInfo) => {
      const res = await api(REPOS_PATH + "/" + encodeURIComponent(r.id), {
        method: "DELETE",
      }).catch((e) => e as Error);
      if (res instanceof Error) {
        setToast({ message: errorMessage(res), type: "error" });
        return;
      }
      if (res.ok) {
        setToast({
          message: tf("Deleted {name}", { name: r.name }),
          type: "success",
        });
        await fetchRepositories();
        return;
      }
      setToast({
        message: await reasonOf(res, t("Could not delete the repository")),
        type: "error",
      });
    },
    [api, setToast, fetchRepositories],
  );

  return {
    repositories,
    fetchRepositories,
    showNewRepository,
    openNewRepository,
    closeNewRepository,
    newRepoUrl,
    setNewRepoUrl,
    newRepoPath,
    setNewRepoPath,
    creatingRepository,
    newRepoError,
    createRepository,
    deleteRepository,
  };
}
