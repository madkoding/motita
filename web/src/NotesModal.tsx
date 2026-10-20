// The notes window: a library of guides and notes on the left, the page being read on the right.
// Guides are what Motita writes about each project; the rest are the person's own notes, as plain
// Markdown files where [[links]] open the note they name.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { ConfirmDelete } from "./NoteMenus";
import { NoteList, SMALL_BTN } from "./NotesLibrary";
import { OpenNote } from "./NoteReader";
import { IconClose } from "./icons";

export function NotesModal() {
  const app = useApp();
  const {
    closeNotes,
    graph,
    openGraph,
    closeGraph,
    pending,
    askDelete,
    confirmDelete,
  } = app.notes;
  const toggleGraph = () => {
    if (graph) {
      closeGraph();
    } else {
      void openGraph();
    }
  };
  return (
    <ModalBackdrop onClose={() => void closeNotes()}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-5xl p-3 md:p-5 shadow-2xl h-[92dvh] md:h-[85vh] flex flex-col">
        <div class="flex items-center gap-2 mb-3">
          <div class="min-w-0 flex-1">
            <h2 class="text-base font-semibold">{t("Notes")}</h2>
            <p class="text-xs text-[#9a9aaa] truncate hidden sm:block">
              {t(
                "Guides to your projects, your conversations and your own ideas.",
              )}
            </p>
          </div>
          <button
            class={SMALL_BTN}
            aria-pressed={!!graph}
            title={t("See how the notes connect to each other")}
            data-testid="note-graph-toggle"
            onClick={toggleGraph}
          >
            {choose(!!graph, t("Close map"), t("Map"))}
          </button>
          <button
            class="p-1.5 rounded-lg hover:bg-white/5"
            onClick={() => void closeNotes()}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="flex flex-1 min-h-0 gap-3">
          <NoteList />
          <OpenNote />
        </div>
      </div>
      {!!pending && (
        <ConfirmDelete
          deletion={pending}
          onCancel={() => askDelete(null)}
          onConfirm={() => void confirmDelete(pending)}
        />
      )}
    </ModalBackdrop>
  );
}
