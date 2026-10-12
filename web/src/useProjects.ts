// Projects: the New project dialog (empty, or cloned from a repository), the edit dialog, the git
// host connections and the git identity the first clone may ask for, podman, and deleting one.
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { t, tf } from './i18n'
import { choose } from './choose'
import { repoNameOf, slugOf } from './appParse'
import { listAccounts, repoShortName, type GitAccount, type GitRepo } from './gitApi'
import type { SessionList } from './useSessionList'
import type { Api } from './prApi'
import type { EditProjectDraft, GitConnectRequest, ProjectInfo, SetStatus, SetToast } from './appTypes'
import { mainRepoOf, reposOf, type RepoDraft } from './projectRepos'
import type { RepoConflict } from './ReposField'
import { BRANCH_LOOKUP_DELAY_MS, CREATE_SESSION_EVENT, HTTP_CONFLICT, HTTP_NO_CONTENT, JSON_HEADERS, NEW_PROJECT_SESSION_DELAY_MS, PROJECTS_PATH } from './appConst'

type NewProjectMode = null | 'empty' | 'git'

// draftOf is what the edit dialog starts from: the project's own values.
function draftOf(p: ProjectInfo): EditProjectDraft {
  return {
    id: p.id,
    title: p.title,
    description: p.description ?? '',
    main: p.main_branch ?? 'main',
    branches: [p.main_branch || 'main'],
    current: p.branch ?? '',
    mergeMethod: p.merge_method ?? '',
    prMaxFixes: p.pr_max_fixes ?? 0,
    autoMerge: p.auto_merge === true,
    autoContinue: p.auto_continue === true,
    mainRepo: mainRepoOf(p),
    repos: reposOf(p),
  }
}

// errorOf reads the gateway's reason out of a refused request.
async function errorOf(res: Response): Promise<string> {
  const err = await res.json().catch(() => ({}))
  return err.error || ''
}

// addRepoTo clones one more repository into a project, and answers the project as it is now, or
// the gateway's reason when it was refused.
async function addRepoTo(api: Api, id: string, repo: RepoDraft): Promise<{ project?: ProjectInfo; error?: string }> {
  const res = await api(PROJECTS_PATH + encodeURIComponent(id) + '/repos', {
    method: 'POST',
    headers: JSON_HEADERS,
    body: JSON.stringify(repo),
  })
  if (!res.ok) { return { error: await errorOf(res) } }
  return { project: await res.json() }
}

// useProjectRepos adds and removes the repositories of the project the edit dialog holds. Both
// happen at once, not on Save: adding one clones it, and the sessions get a worktree of it.
function useProjectRepos(api: Api, fetchProjects: () => Promise<unknown>, setToast: SetToast,
  setEditProject: (f: (p: EditProjectDraft | null) => EditProjectDraft | null) => void, id: string) {
  // repoBusy is '+' while one is being added, or the name of the one being removed.
  const [repoBusy, setRepoBusy] = useState('')
  const [repoConflict, setRepoConflict] = useState<RepoConflict | null>(null)
  const settle = (p: ProjectInfo) => {
    setEditProject(prev => prev?.id === p.id ? { ...prev, repos: reposOf(p) } : prev)
    void fetchProjects()
  }

  const addProjectRepo = useCallback(async (repo: RepoDraft) => {
    setRepoBusy('+')
    try {
      const out = await addRepoTo(api, id, repo)
      if (!out.project) {
        setToast({ message: tf('Could not add {name}', { name: repo.name }), type: 'error', detail: out.error })
        return false
      }
      settle(out.project)
      return true
    } catch {
      setToast({ message: tf('Could not add {name}', { name: repo.name }), type: 'error' })
      return false
    } finally {
      setRepoBusy('')
    }
  }, [id])

  // removeProjectRepo asks first without force: uncommitted work comes back as a question, and
  // only the person's own answer sends it again with force.
  const removeProjectRepo = useCallback(async (name: string, force: boolean) => {
    setRepoBusy(name)
    setRepoConflict(null)
    try {
      const res = await api(PROJECTS_PATH + encodeURIComponent(id) + '/repos/' + encodeURIComponent(name) + choose(force, '?force=1', ''), { method: 'DELETE' })
      if (res.status === HTTP_CONFLICT) {
        setRepoConflict({ name, message: await errorOf(res) })
        return
      }
      if (!res.ok) {
        setToast({ message: tf('Could not remove {name}', { name }), type: 'error', detail: await errorOf(res) })
        return
      }
      settle(await res.json())
    } catch {
      setToast({ message: tf('Could not remove {name}', { name }), type: 'error' })
    } finally {
      setRepoBusy('')
    }
  }, [id])

  return { repoBusy, repoConflict, addProjectRepo, removeProjectRepo }
}

// withBranches puts what the gateway says about a project's branches into the edit dialog.
function withBranches(prev: EditProjectDraft, d: { branches?: string[]; main_branch?: string; current?: string }): EditProjectDraft {
  return { ...prev, branches: choose(d.branches?.length, d.branches as string[], prev.branches), main: d.main_branch || prev.main, current: d.current ?? prev.current }
}

// openSessionIn asks the page to open a session in a project that was just created, so the user
// lands in the chat.
function openSessionIn(projectId: string) {
  setTimeout(() => {
    const evt = new CustomEvent(CREATE_SESSION_EVENT, { detail: { project_id: projectId } })
    window.dispatchEvent(evt)
  }, NEW_PROJECT_SESSION_DELAY_MS)
}

// useEditProject is the edit-project dialog: what is being edited, the branches its selector
// offers, and saving it.
function useEditProject(api: Api, fetchProjects: () => Promise<unknown>, setToast: SetToast) {
  const [editProject, setEditProject] = useState<EditProjectDraft | null>(null)
  const [savingProject, setSavingProject] = useState(false)

  // openEditProject opens the edit dialog with the project's own values, and asks the gateway
  // which branches the main-branch selector can offer.
  const openEditProject = useCallback(async (p: ProjectInfo) => {
    setEditProject(draftOf(p))
    try {
      const res = await api(PROJECTS_PATH + encodeURIComponent(p.id) + '/branches')
      if (!res.ok) { return }
      const d = await res.json()
      setEditProject(prev => {
        if (prev?.id !== p.id) { return prev }
        return withBranches(prev, d)
      })
    } catch { /* the selector keeps the branch it already knows */ }
  }, [])

  // saveEditProject sends what the dialog holds. The gateway puts the project back on its main
  // branch when it has no sessions, so the list is read again afterwards.
  const saveEditProject = useCallback(async () => {
    if (!editProject?.title.trim()) { return }
    setSavingProject(true)
    try {
      const res = await api(PROJECTS_PATH + encodeURIComponent(editProject.id), {
        method: 'PATCH',
        headers: JSON_HEADERS,
        body: JSON.stringify({ title: editProject.title, description: editProject.description, main_branch: editProject.main, merge_method: editProject.mergeMethod, pr_max_fixes: editProject.prMaxFixes, auto_merge: editProject.autoMerge, auto_continue: editProject.autoContinue }),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({}))
        setToast({ message: t('Could not save the project'), type: 'error', detail: err.error })
        return
      }
      setEditProject(null)
      await fetchProjects()
      setToast({ message: t('Project saved'), type: 'success' })
    } catch {
      setToast({ message: t('Could not save the project'), type: 'error' })
    } finally {
      setSavingProject(false)
    }
  }, [editProject, fetchProjects])

  // patchEditProject changes one field of the edit dialog.
  const patchEditProject = useCallback((patch: Partial<EditProjectDraft>) => {
    setEditProject(p => p && { ...p, ...patch })
  }, [])
  const closeEditProject = useCallback(() => setEditProject(null), [])
  const repos = useProjectRepos(api, fetchProjects, setToast, setEditProject, editProject?.id ?? '')

  return { editProject, patchEditProject, closeEditProject, savingProject, openEditProject, saveEditProject, ...repos }
}

interface Deps {
  api: Api
  lists: SessionList
  setState: SetStatus
  setToast: SetToast
}

export function useProjects({ api, lists, setState, setToast }: Deps) {
  const { fetchProjects, fetchSessions } = lists
  const edit = useEditProject(api, fetchProjects, setToast)
  const [showNewProject, setShowNewProject] = useState(false)
  // How the new project starts: null is the first screen ("empty or from a repository?").
  const [newProjectMode, setNewProjectMode] = useState<NewProjectMode>(null)
  const [newProjectTitle, setNewProjectTitle] = useState('')
  const [newProjectDesc, setNewProjectDesc] = useState('')
  const [newProjectDir, setNewProjectDir] = useState('')
  const [newProjectGit, setNewProjectGit] = useState('')
  // The other repositories the new project gets once it exists, after the main one.
  const [newProjectRepos, setNewProjectRepos] = useState<RepoDraft[]>([])
  // The branch the new project treats as its main line: main unless the person picks master.
  const [newProjectMain, setNewProjectMain] = useState('main')
  // The branch the remote repository's HEAD points to, once asked; and whether the person has
  // already chosen a main branch themselves (a detected one must never overwrite that).
  const [remoteMain, setRemoteMain] = useState('')
  const mainChosen = useRef(false)
  // Podman: whether this machine has it (asked when the dialog opens) and the user's answer.
  const [hasPodman, setHasPodman] = useState(false)
  const [hasPodmanCompose, setHasPodmanCompose] = useState(false)
  // Installed is not the same as working: `podman info` can fail on a machine that has the binary.
  const [podmanReady, setPodmanReady] = useState(false)
  const [newProjectPodman, setNewProjectPodman] = useState(false)
  // The git identity dialog: opened when the gateway answers that git has no user, and
  // submitted together with the project it interrupted.
  const [showGitIdentity, setShowGitIdentity] = useState(false)
  // Git host connections: the connect modal (from Settings, the New project dialog or a refused
  // clone), the repository picker, and the hosts that are connected right now.
  // `after` says what to do once the host is connected: pick a repository, or send the create again.
  const [gitConnect, setGitConnect] = useState<GitConnectRequest | null>(null)
  const [showRepoPicker, setShowRepoPicker] = useState(false)
  const [gitAccounts, setGitAccounts] = useState<GitAccount[] | null>(null)
  const [gitRev, setGitRev] = useState(0)
  const createProjectRef = useRef<() => void>(() => {})
  const [gitUserName, setGitUserName] = useState('')
  const [gitUserEmail, setGitUserEmail] = useState('')
  const [creatingProject, setCreatingProject] = useState(false)

  // resetNewProject empties the New project dialog once the project exists.
  const resetNewProject = () => {
    setShowNewProject(false)
    setNewProjectTitle('')
    setNewProjectDesc('')
    setNewProjectDir('')
    setNewProjectGit('')
    setNewProjectRepos([])
    setNewProjectMode(null)
    mainChosen.current = false
    setNewProjectMain('main')
    setNewProjectPodman(false)
    setShowGitIdentity(false)
    setCreatingProject(false)
  }

  // createRefused handles the gateway's refusal to create the project. A clone refused for
  // credentials connects that host and a git with no user asks for one: both send the same
  // request again afterwards.
  const createRefused = async (res: Response) => {
    const err = await res.json().catch(() => ({}))
    setCreatingProject(false)
    if (res.status === HTTP_CONFLICT && err.code === 'git_auth_required') {
      setGitConnect({ service: err.service || undefined, after: 'create' })
      return
    }
    if (res.status === HTTP_CONFLICT && err.code === 'git_identity_required') {
      // Nothing was created.
      setShowGitIdentity(true)
      return
    }
    setState(err.error || 'could not create the project', true)
  }

  // createProject sends a new project to the gateway. When a git URL is set,
  // the gateway clones the repo and returns the clone log, which we show in
  // the modal so the user can see what happened.
  const createProject = useCallback(async (identity?: { name: string; email: string }) => {
    // An empty project ignores whatever was typed in the repository field before the person went
    // back; a project from a repository takes its name and folder from the repository.
    const gitUrl = choose(newProjectMode === 'git', newProjectGit.trim(), '')
    const fromUrl = choose(gitUrl, repoNameOf(gitUrl), '')
    const title = newProjectTitle.trim() || fromUrl
    const dir = newProjectDir.trim() || slugOf(fromUrl) || slugOf(title)
    if (!title || !dir) { return }
    setCreatingProject(true)
    try {
      const res = await api('/v1/projects', {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify({
          title,
          description: newProjectDesc.trim(),
          dir,
          git_url: gitUrl || undefined,
          use_podman: choose(hasPodman && newProjectPodman, true, undefined),
          main_branch: newProjectMain,
          git_user_name: identity?.name,
          git_user_email: identity?.email,
        })
      })
      if (!res.ok) {
        await createRefused(res)
        return
      }
      const data = await res.json()
      const failed = await addReposToNew(data.id)
      await fetchProjects()
      // Auto-create a session in the new project so the user lands in the chat.
      if (data.id) { openSessionIn(data.id) }
      // Show a toast and close the modal.
      if (failed.length > 0) {
        setToast({ message: tf('Project "{title}" created, but some repositories could not be added: {names}', { title, names: failed.join(', ') }), type: 'warning' })
      } else {
        setToast({ message: tf(choose(data.clone_log, 'Project "{title}" created and repo cloned', 'Project "{title}" created'), { title }), type: 'success' })
      }
      resetNewProject()
    } catch (e) {
      setState(tf('could not create the project: {err}', { err: String(e) }), true)
      setCreatingProject(false)
    }
  }, [newProjectMode, newProjectTitle, newProjectDesc, newProjectDir, newProjectGit, newProjectMain, newProjectPodman, hasPodman, newProjectRepos, fetchProjects])

  // addReposToNew clones the other repositories into the project just created, one after the
  // other, and answers the names of the ones that could not be added.
  async function addReposToNew(id: string | undefined): Promise<string[]> {
    const failed: string[] = []
    for (const repo of newProjectRepos) {
      const out = await addRepoTo(api, id || '', repo).catch(() => ({ project: undefined }))
      if (!out.project) { failed.push(repo.name) }
    }
    return failed
  }

  // addNewProjectRepo and removeNewProjectRepo change the list of other repositories the New
  // project dialog holds. Nothing is cloned until the project is created.
  const addNewProjectRepo = useCallback((repo: RepoDraft) => {
    setNewProjectRepos(list => [...list, repo])
    return true
  }, [])
  const removeNewProjectRepo = useCallback((name: string) => {
    setNewProjectRepos(list => list.filter(r => r.name !== name))
  }, [])

  createProjectRef.current = () => { void createProject() }

  // The hosts that are connected, for the New project dialog. Read when it opens and after a connect.
  useEffect(() => {
    if (!showNewProject) { return undefined }
    let live = true
    listAccounts(api).then(a => { if (live) { setGitAccounts(a) } }).catch(() => { if (live) { setGitAccounts([]) } })
    return () => { live = false }
  }, [showNewProject, gitRev])

  // When a repository URL is typed, ask its remote which branch it uses and offer that one: a
  // repository on develop or master should not need the person to know to change the selector.
  // Debounced, because the field changes on every key; and silent on failure, because the
  // selector works without the answer (the gateway settles on a branch when it clones).
  useEffect(() => {
    setRemoteMain('')
    const url = newProjectGit.trim()
    if (!showNewProject || newProjectMode !== 'git' || !repoNameOf(url)) { return undefined }
    let live = true
    const lookup = async () => {
      try {
        const res = await api('/v1/git/default-branch?url=' + encodeURIComponent(url))
        if (!res.ok || !live) { return }
        const d = await res.json()
        if (!live || !d.branch) { return }
        setRemoteMain(d.branch)
        if (!mainChosen.current) { setNewProjectMain(d.branch) }
      } catch { /* the selector keeps what it has */ }
    }
    const timer = setTimeout(lookup, BRANCH_LOOKUP_DELAY_MS)
    return () => { live = false; clearTimeout(timer) }
  }, [showNewProject, newProjectMode, newProjectGit])

  // Ask the gateway which container runtimes the machine has, so podman is only offered when it is there.
  // Read on start (the project menu needs it) and again when the dialog opens.
  useEffect(() => {
    let live = true
    const set = (d: { podman?: boolean; podman_compose?: boolean; podman_ready?: boolean }) => {
      setHasPodman(!!d.podman)
      setHasPodmanCompose(!!d.podman_compose)
      setPodmanReady(!!d.podman_ready)
    }
    api('/v1/runtimes').then(r => r.json()).then(d => {
      if (live) { set(d) }
    }).catch(() => { if (live) { set({}) } })
    return () => { live = false }
  }, [showNewProject])

  // setProjectPodman changes the answer given at creation: the gateway tells the open sessions.
  const setProjectPodman = useCallback(async (id: string, podman: boolean) => {
    try {
      const res = await api(PROJECTS_PATH + id, {
        method: 'PATCH',
        headers: JSON_HEADERS,
        body: JSON.stringify({ podman })
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({}))
        setState(err.error || 'could not change the project', true)
        return
      }
      await fetchProjects()
    } catch {
      setState('could not change the project', true)
    }
  }, [fetchProjects])


  const onGitConnected = useCallback((_service: string, account: string) => {
    const after = gitConnect?.after
    setGitRev(n => n + 1)
    setToast({ message: choose(account, tf('Connected as {user}', { user: account }), t('Connected')), type: 'success' })
    if (after === 'picker') { setShowRepoPicker(true) }
    if (after === 'create') { setTimeout(() => createProjectRef.current(), 0) }
  }, [gitConnect])

  // pickRepo fills the New project form from a repository the user chose.
  const pickRepo = useCallback((r: GitRepo) => {
    const name = repoShortName(r)
    setNewProjectMode('git')
    setNewProjectGit(r.clone_url)
    setNewProjectTitle(prev => choose(prev.trim(), prev, name))
    setNewProjectDesc(prev => choose(prev.trim(), prev, (r.description || '')))
    setShowRepoPicker(false)
  }, [])

  // deleteProject removes a project from the gateway.
  // `discard` carries the user's answer, exactly as it does for a session: a project
  // whose sessions hold uncommitted work is refused without it.
  const deleteProject = useCallback(async (id: string, discard = false) => {
    try {
      const res = await api(PROJECTS_PATH + id + (choose(discard, '?force=1', '')), { method: 'DELETE' })
      if (!res.ok && res.status !== HTTP_NO_CONTENT) {
        const err = await res.json().catch(() => ({}))
        setState(err.error || 'could not delete the project', true)
        return
      }
      await fetchProjects()
      await fetchSessions()
    } catch { /* ignore */ }
  }, [fetchProjects, fetchSessions])

  // openNewProject opens the New project dialog on its first screen.
  const openNewProject = () => {
    setNewProjectMode(null)
    mainChosen.current = false
    setNewProjectMain('main')
    setShowNewProject(true)
  }
  const closeNewProject = useCallback(() => setShowNewProject(false), [])
  // chooseMain is the person picking the main branch: a detected one no longer replaces it.
  const chooseMain = (branch: string) => {
    mainChosen.current = true
    setNewProjectMain(branch)
  }
  const closeGitIdentity = useCallback(() => setShowGitIdentity(false), [])
  const closeRepoPicker = useCallback(() => setShowRepoPicker(false), [])
  const connectAnother = useCallback(() => setGitConnect({ after: 'picker' }), [])
  const pickerAuthRequired = useCallback((service: string) => {
    setShowRepoPicker(false)
    setGitConnect({ service, after: 'picker' })
  }, [])
  const closeGitConnect = useCallback(() => setGitConnect(null), [])

  return {
    showNewProject, openNewProject, closeNewProject, newProjectMode, setNewProjectMode,
    newProjectTitle, setNewProjectTitle, newProjectDesc, setNewProjectDesc, newProjectDir, setNewProjectDir,
    newProjectGit, setNewProjectGit, newProjectMain, chooseMain, remoteMain,
    newProjectRepos, addNewProjectRepo, removeNewProjectRepo,
    ...edit,
    hasPodman, hasPodmanCompose, podmanReady, newProjectPodman, setNewProjectPodman, setProjectPodman,
    showGitIdentity, closeGitIdentity, gitUserName, setGitUserName, gitUserEmail, setGitUserEmail,
    gitConnect, setGitConnect, closeGitConnect, onGitConnected, showRepoPicker, setShowRepoPicker, closeRepoPicker,
    connectAnother, pickerAuthRequired, gitAccounts, gitRev, pickRepo,
    creatingProject, createProject, deleteProject,
  }
}

export type Projects = ReturnType<typeof useProjects>
