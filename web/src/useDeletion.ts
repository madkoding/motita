// The delete confirmation: what is about to be deleted, what the deletion would discard, and the
// deletion itself once the person confirms.
import { useCallback, useEffect, useState } from 'preact/hooks'
import { choose } from './choose'
import type { Api } from './prApi'
import type { ConfirmTarget, DeletionPreview } from './appTypes'
import { PROJECTS_PATH, SESSIONS_PATH } from './appConst'

interface Deps {
  api: Api
  deleteSession: (id: string, discard?: boolean) => Promise<void>
  deleteSkill: (name: string) => Promise<void>
  deleteProject: (id: string, discard?: boolean) => Promise<void>
}

// previewPathOf is where the gateway answers what deleting a session or a project would discard.
function previewPathOf(target: ConfirmTarget): string {
  return choose(target.type === 'session', SESSIONS_PATH + target.id + '/deletion-preview', PROJECTS_PATH + target.id + '/deletion-preview')
}

// readPreview reads the preview, or null when the gateway gave none.
async function readPreview(res: Response) {
  if (!res.ok) { return null }
  return res.json().catch(() => null)
}

export function useDeletion({ api, deleteSession, deleteSkill, deleteProject }: Deps) {
  // Confirm-delete modal: when set, shows a modal asking the user to confirm.
  const [confirmDelete, setConfirmDelete] = useState<ConfirmTarget | null>(null)
  // What the deletion would DISCARD, asked of the gateway when the modal opens.
  //
  // The modal used to promise only "this will be deleted", so a session whose
  // checkout held uncommitted work could not be deleted at all: the gateway
  // refused, the refusal arrived after the fact, and nothing in the interface
  // said which files were in the way. Now the modal asks first, names them, and
  // the confirm button carries the answer.
  //
  // `checking` is a real state, not decoration: the modal must not offer to
  // discard before it knows whether there is anything to discard.
  const [deletionPreview, setDeletionPreview] = useState<DeletionPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  // True while the deletion the user confirmed is IN FLIGHT.
  //
  // A deletion is not instant: the gateway stops the session's run first, then
  // gives its checkout back to git, and on a project it does that for every
  // session it owns. Measured on a project with two sessions holding work: the
  // request took long enough that a second click was possible, and without this
  // the modal sat there looking idle while the work was already under way.
  // Locking the button and showing that something is happening is the honest
  // report of a request in flight.
  const [deleting, setDeleting] = useState(false)

  // The deletion preview is fetched when the modal OPENS, so the dialog can say what
  // will be lost before anything is decided.
  //
  // It is fetched for a session only: a project's deletion is its own decision, and
  // the gateway answers the same question for it separately. A failed request is not
  // treated as "nothing to lose" — the modal then falls back to asking the plain
  // confirmation, and the gateway's own refusal still protects the work.
  useEffect(() => {
    if (!confirmDelete) {
      setDeletionPreview(null)
      // Reset with the modal: a fresh confirmation must start unlocked, whatever
      // the previous one left behind.
      setDeleting(false)
      return undefined
    }
    // Both kinds ask the same question of their own endpoint: a session names its own
    // checkout's changes, a project names each of its sessions' changes. A skill has no
    // checkout, so there is nothing to ask.
    if (confirmDelete.type === 'skill') {
      setDeletionPreview(null)
      return undefined
    }
    let cancelled = false
    setPreviewLoading(true)
    api(previewPathOf(confirmDelete))
      .then(readPreview)
      .then((data) => {
        if (cancelled) { return }
        setDeletionPreview(choose(data && typeof data === 'object', data, null))
      })
      .catch(() => { if (!cancelled) { setDeletionPreview(null) } })
      .finally(() => { if (!cancelled) { setPreviewLoading(false) } })
    return () => { cancelled = true }
  }, [confirmDelete])

  // runDelete deletes what the modal is about.
  //
  // The confirmation IS the authorisation to discard. It used to be sent only
  // when the preview had counted changes, so a checkout whose changes arrived
  // after the preview (the run being stopped by this very deletion), or whose
  // preview could not be read, was refused with a git error AFTER the user had
  // said yes.
  const runDelete = (target: ConfirmTarget) => {
    if (target.type === 'session') { return deleteSession(target.id, true) }
    if (target.type === 'skill') { return deleteSkill(target.id) }
    return deleteProject(target.id, true)
  }

  // confirmDeletion is the confirm button. It is locked FIRST, so a second click cannot start a
  // second deletion while the first is still unwinding on the gateway.
  const confirmDeletion = async () => {
    if (!confirmDelete) { return }
    setDeleting(true)
    try {
      await runDelete(confirmDelete)
    } finally {
      setDeleting(false)
      setConfirmDelete(null)
    }
  }

  const cancelDelete = useCallback(() => setConfirmDelete(null), [])

  return { confirmDelete, setConfirmDelete, cancelDelete, deletionPreview, previewLoading, deleting, confirmDeletion }
}

export type Deletion = ReturnType<typeof useDeletion>
