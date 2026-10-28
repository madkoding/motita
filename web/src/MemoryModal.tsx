// The memory window: the search and the library of entries on the left, the page being read on the
// right. Entries are plain Markdown files kept in collections, where [[links]] open the entry they
// name.
import { useCallback } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { ConfirmDelete } from "./MemoryMenus";
import { MemoryList, SMALL_BTN } from "./MemoryLibrary";
import { OpenEntry } from "./MemoryReader";
import { IconClose } from "./icons";

export function MemoryModal() {
  const app = useApp();
  const {
    closeMemory,
    graph,
    openGraph,
    closeGraph,
    pending,
    askDelete,
    confirmDelete,
  } = app.memory;
  const toggleGraph = () => {
    if (graph) {
      closeGraph();
    } else {
      void openGraph();
    }
  };
  const close = useCallback(() => void closeMemory(), [closeMemory]);
  const confirmPending = useCallback(
    () => pending && void confirmDelete(pending),
    [pending, confirmDelete],
  );
  const cancelDelete = useCallback(() => askDelete(null), [askDelete]);
  return (
    <ModalBackdrop onClose={close}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-5xl p-3 md:p-5 shadow-2xl h-[92dvh] md:h-[85vh] flex flex-col">
        <div class="flex items-center gap-2 mb-3">
          <div class="min-w-0 flex-1">
            <h2 class="text-base font-semibold">{t("Memory")}</h2>
            <p class="text-xs text-[#9a9aaa] truncate hidden sm:block">
              {t("What you and Motita want to remember, found by meaning.")}
            </p>
          </div>
          <button
            class={SMALL_BTN}
            aria-pressed={!!graph}
            title={t("See how the entries connect to each other")}
            data-testid="memory-graph-toggle"
            onClick={toggleGraph}
          >
            {choose(!!graph, t("Close map"), t("Map"))}
          </button>
          <button
            class="p-1.5 rounded-lg hover:bg-white/5"
            onClick={close}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="flex flex-1 min-h-0 gap-3">
          <MemoryList />
          <OpenEntry />
        </div>
      </div>
      {!!pending && (
        <ConfirmDelete
          deletion={pending}
          onCancel={cancelDelete}
          onConfirm={confirmPending}
        />
      )}
    </ModalBackdrop>
  );
}
