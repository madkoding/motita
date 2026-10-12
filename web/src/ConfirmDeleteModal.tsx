// The delete confirmation, which asks before deleting a session, a project or a skill, and the
// long-press menu a row opens on a phone.
import { t, tf, plural } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { deleteWarning, previewKind } from "./appFormat";
import { IconEdit, IconTrash } from "./icons";
import type { ConfirmTarget, DeletionPreview, FileChange } from "./appTypes";
import { CONTEXT_MENU_H, CONTEXT_MENU_W, NEW_SESSION_TITLE } from "./appConst";

const NOTE = "text-xs text-[#9a9aaa] mb-5";
const DANGER_BOX = "mb-5 rounded-xl border border-danger/40 bg-danger/10 p-3";

// discardCount says how many uncommitted changes the deletion discards.
function discardCount(n: number): string {
  return plural(
    n,
    "1 uncommitted change will be discarded",
    "{n} uncommitted changes will be discarded",
  );
}

// headingOf is the modal's title, by what is being deleted.
function headingOf(type: ConfirmTarget["type"]): string {
  if (type === "session") {
    return t("Delete session");
  }
  return t(choose(type === "project", "Delete project", "Delete skill"));
}

// ChangeLine is one uncommitted change, a rename showing where it came from.
function ChangeLine({ c, indent = "" }: { c: FileChange; indent?: string }) {
  return (
    <li class={`flex items-start gap-2 text-xs${indent}`}>
      <span class="text-[#9a9aaa] font-mono flex-none w-18">{c.kind}</span>
      <span class="text-[#e8e8ea] font-mono break-all">
        {choose(c.from, c.from + " → " + c.path, c.path)}
      </span>
    </li>
  );
}

// InspectionFailed says a checkout could not be read, because an empty list would read as
// "nothing to lose".
function InspectionFailed({ title, error }: { title: string; error?: string }) {
  return (
    <div class={DANGER_BOX}>
      <p class="text-xs text-[#e8e8ea] font-semibold mb-1">{title}</p>
      <p class="text-xs text-[#9a9aaa] wrap-break-word">{error}</p>
      <p class="text-xs text-[#9a9aaa] mt-2">
        {t(
          "Its contents cannot be shown. Deleting discards whatever it holds, uncommitted work included.",
        )}
      </p>
    </div>
  );
}

// SessionPreview is what deleting a session would DISCARD. The gateway is asked when the modal
// opens, and its answer is shown here: a count warns, but only the list lets someone decide.
function SessionPreview({
  preview,
  loading,
}: {
  preview: DeletionPreview | null;
  loading: boolean;
}) {
  const kind = previewKind(loading, preview);
  return (
    <>
      {loading && (
        <p class={NOTE}>{t("Checking what this session is holding…")}</p>
      )}
      {!!preview && kind === "failed" && (
        <InspectionFailed
          title={t("This session's checkout could not be inspected")}
          error={preview.error}
        />
      )}
      {!!preview && kind === "changes" && (
        <div class={DANGER_BOX}>
          <p class="text-xs text-[#e8e8ea] font-semibold mb-2">
            {discardCount(preview.count)}
            {!!preview.branch &&
              " " + tf("in {branch}", { branch: preview.branch })}
          </p>
          <ul class="space-y-1 max-h-40 overflow-y-auto">
            {preview.changes.map((c) => (
              <ChangeLine key={c.path} c={c} />
            ))}
          </ul>
          <p class="text-xs text-[#9a9aaa] mt-2">
            {t(
              "These files are not committed anywhere. Deleting the session discards them for good.",
            )}
          </p>
        </div>
      )}
      {!!preview?.worktree && kind === "clean" && (
        <p class={NOTE}>
          {t(
            "The session's checkout is clean, so nothing uncommitted is lost.",
          )}
        </p>
      )}
    </>
  );
}

// ProjectPreview is the same for a project. Its sessions each hold their own checkout, so the
// list is grouped by session: this is what "and all its sessions" actually costs.
function ProjectPreview({
  preview,
  loading,
}: {
  preview: DeletionPreview | null;
  loading: boolean;
}) {
  const kind = previewKind(loading, preview);
  return (
    <>
      {loading && (
        <p class={NOTE}>
          {t("Checking what this project's sessions are holding…")}
        </p>
      )}
      {!!preview && kind === "failed" && (
        <InspectionFailed
          title={t("A session's checkout could not be inspected")}
          error={preview.error}
        />
      )}
      {!!preview && kind === "changes" && (
        <div class={DANGER_BOX}>
          <p class="text-xs text-[#e8e8ea] font-semibold mb-2">
            {discardCount(preview.count)}
          </p>
          <div class="space-y-2 max-h-48 overflow-y-auto">
            {(preview.sessions || [])
              .filter((s) => s.changes.length > 0)
              .map((s) => (
                <div key={s.id}>
                  <p class="text-xs text-[#9a9aaa] truncate">
                    {choose(
                      s.title === NEW_SESSION_TITLE,
                      t(NEW_SESSION_TITLE),
                      s.title || s.id,
                    )}
                  </p>
                  <ul class="space-y-1 mt-1">
                    {s.changes.map((c) => (
                      <ChangeLine key={c.path} c={c} indent=" pl-2" />
                    ))}
                  </ul>
                </div>
              ))}
          </div>
          <p class="text-xs text-[#9a9aaa] mt-2">
            {t(
              "These files are not committed anywhere. Deleting the project discards them for good.",
            )}
          </p>
        </div>
      )}
      {kind === "clean" && (
        <p class={NOTE}>
          {t(
            "No session holds uncommitted changes, so nothing uncommitted is lost. The project's own checkout is not touched.",
          )}
        </p>
      )}
    </>
  );
}

// asksPreview says the target has a checkout whose contents the modal shows before deleting.
function asksPreview(target: ConfirmTarget): boolean {
  if (target.type === "project") {
    return true;
  }
  return target.type === "session" && target.id !== "default";
}

// confirmLabel is what the confirm button says: the deletion under way, or whether it discards.
function confirmLabel(
  deleting: boolean,
  preview: DeletionPreview | null,
): string {
  if (deleting) {
    return t("Deleting…");
  }
  const discards =
    !!preview && (preview.count > 0 || !!preview.inspection_failed);
  return t(choose(discards, "Discard and delete", "Delete"));
}

export function ConfirmDeleteModal({ target }: { target: ConfirmTarget }) {
  const {
    cancelDelete,
    deletionPreview,
    previewLoading,
    deleting,
    confirmDeletion,
  } = useApp().deletion;
  const waiting = asksPreview(target) && previewLoading;
  return (
    <ModalBackdrop onClose={cancelDelete}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-sm p-5 shadow-2xl">
        <div class="flex items-center gap-2 mb-3">
          <div class="w-10 h-10 rounded-full bg-danger/15 flex items-center justify-center flex-none">
            <IconTrash size={20} class="text-danger" />
          </div>
          <div class="flex-1 min-w-0">
            <h2 class="text-base font-semibold">{headingOf(target.type)}</h2>
            <p class="text-sm text-[#9a9aaa] truncate">{target.title}</p>
          </div>
        </div>
        <p class="text-sm text-[#9a9aaa] mb-5">
          {deleteWarning(target.type, target.id)}
        </p>

        {target.type === "session" && target.id !== "default" && (
          <SessionPreview preview={deletionPreview} loading={previewLoading} />
        )}
        {target.type === "project" && (
          <ProjectPreview preview={deletionPreview} loading={previewLoading} />
        )}

        <div class="flex gap-2">
          <button
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-danger text-white font-semibold active:scale-95 transition-transform disabled:opacity-50 disabled:cursor-not-allowed inline-flex items-center justify-center gap-2"
            disabled={deleting || waiting}
            onClick={confirmDeletion}
          >
            {deleting && (
              // The same ring the sidebar uses for a run in flight, so "something is happening"
              // reads the same wherever it appears.
              <span class="state-working inline-block" aria-hidden="true" />
            )}
            {confirmLabel(deleting, deletionPreview)}
          </button>
          <button
            class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform disabled:opacity-50 disabled:cursor-not-allowed"
            disabled={deleting}
            onClick={cancelDelete}
          >
            {t("Cancel")}
          </button>
        </div>
      </div>
    </ModalBackdrop>
  );
}

const MENU_ITEM =
  "w-full flex items-center gap-2 px-4 py-2.5 text-sm transition-colors";

// ContextMenu is the long-press menu of a session or a project row: rename and delete.
export function ContextMenu() {
  const { menus, actions, deletion } = useApp();
  const { contextMenu, closeContextMenu } = menus;
  if (!contextMenu) {
    return null;
  }
  return (
    <ModalBackdrop onClose={closeContextMenu} class="fixed inset-0 z-50">
      <div
        class="frosted rounded-xl border border-white/10 shadow-2xl py-1 min-w-[160px]"
        style={{
          position: "fixed",
          left: `${Math.min(contextMenu.x, window.innerWidth - CONTEXT_MENU_W)}px`,
          top: `${Math.min(contextMenu.y, window.innerHeight - CONTEXT_MENU_H)}px`,
        }}
      >
        <button
          class={MENU_ITEM + " text-[#e8e8ea] hover:bg-white/5"}
          onClick={() => {
            if (contextMenu.type === "session") {
              actions.startRename(contextMenu.id, contextMenu.title);
            }
            closeContextMenu();
          }}
        >
          <IconEdit size={16} />
          {t("Rename")}
        </button>
        <button
          class={MENU_ITEM + " text-danger hover:bg-danger/10"}
          onClick={() => {
            deletion.setConfirmDelete({
              type: contextMenu.type,
              id: contextMenu.id,
              title: contextMenu.title,
            });
            closeContextMenu();
          }}
        >
          <IconTrash size={16} />
          {t("Delete")}
        </button>
      </div>
    </ModalBackdrop>
  );
}
