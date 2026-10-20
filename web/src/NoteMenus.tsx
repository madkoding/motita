// What the notes window asks of the person: the "⋯" menu of a folder or a note, and the question
// before anything is deleted.
import { useRef, useState } from "preact/hooks";
import { plural, t } from "./i18n";
import { choose } from "./choose";
import { BodyPortal } from "./BodyPortal";
import { FloatingMenu } from "./FloatingMenu";
import { IconDots, IconTrash } from "./icons";
import { ModalBackdrop } from "./ModalBackdrop";

// MenuItem is one thing a "⋯" menu does; hint says in a few words what it will do.
export interface MenuItem {
  label: string;
  hint: string;
  danger?: boolean;
  testid: string;
  onSelect: () => void;
}

// DotsMenu is a "⋯" button that opens a short list of what can be done. A click anywhere else
// closes it, and so does choosing an item.
export function DotsMenu({
  label,
  testid,
  items,
}: {
  label: string;
  testid: string;
  items: MenuItem[];
}) {
  const button = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  function close() {
    setOpen(false);
  }
  return (
    <>
      <button
        ref={button}
        type="button"
        class="flex-none flex items-center justify-center min-w-[32px] min-h-[32px] rounded-lg text-[#9a9aaa] hover:text-[#e8e8ea] hover:bg-white/5"
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        data-testid={testid}
        onClick={(e) => {
          // Inside a folder's title: opening the menu must not fold the folder.
          e.preventDefault();
          setOpen(!open);
        }}
      >
        <IconDots size={16} />
      </button>
      {open && (
        <BodyPortal>
          <ModalBackdrop
            onClose={close}
            class="fixed inset-0 z-[55]"
            modal={false}
          >
            <FloatingMenu anchor={button.current} class="cp-menu">
              {items.map((item) => (
                <button
                  key={item.testid}
                  type="button"
                  role="menuitem"
                  class={choose(!!item.danger, "is-danger", "")}
                  data-testid={item.testid}
                  onClick={() => {
                    setOpen(false);
                    item.onSelect();
                  }}
                >
                  {item.label}
                  <small>{item.hint}</small>
                </button>
              ))}
            </FloatingMenu>
          </ModalBackdrop>
        </BodyPortal>
      )}
    </>
  );
}

// Deletion is what the person is about to delete: a note, or a whole folder with how many notes it
// holds. A folder a scan writes comes back with the next update, so it says so.
export interface Deletion {
  kind: "note" | "folder";
  path: string;
  name: string;
  count: number;
  rewritable: boolean;
}

// WARNING says what a deletion costs, by what is deleted.
function warning(d: Deletion): string {
  if (d.kind === "note") {
    return t("The note is deleted for good, with everything written in it.");
  }
  if (d.rewritable) {
    return t(
      "Everything in it is deleted, including what you wrote in these notes. Motita can write it again from your project when you update the guides.",
    );
  }
  return t(
    "Everything in it is deleted, including what you wrote in these notes. This cannot be undone.",
  );
}

// ConfirmDelete asks before a note or a folder of notes is deleted, saying what goes with it.
export function ConfirmDelete({
  deletion,
  onCancel,
  onConfirm,
}: {
  deletion: Deletion;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const folder = deletion.kind === "folder";
  return (
    <BodyPortal>
      <ModalBackdrop
        onClose={onCancel}
        class="fixed inset-0 bg-black/60 backdrop-blur-xs z-[56] flex items-center justify-center p-4"
      >
        <div
          class="frosted rounded-2xl border border-white/10 w-full max-w-sm p-5 shadow-2xl"
          data-testid="note-confirm"
        >
          <div class="flex items-center gap-2 mb-3">
            <div class="min-w-[40px] min-h-[40px] rounded-full bg-danger/15 flex items-center justify-center flex-none">
              <IconTrash size={20} class="text-danger" />
            </div>
            <div class="flex-1 min-w-0">
              <h2 class="text-base font-semibold">
                {choose(
                  folder,
                  t("Delete this folder of notes?"),
                  t("Delete this note?"),
                )}
              </h2>
              <p class="text-sm text-[#9a9aaa] truncate">{deletion.name}</p>
            </div>
          </div>
          {folder && (
            <p
              class="text-sm text-[#e8e8ea] mb-1"
              data-testid="note-confirm-count"
            >
              {plural(
                deletion.count,
                "1 note will be deleted.",
                "{n} notes will be deleted.",
              )}
            </p>
          )}
          <p class="text-sm text-[#9a9aaa] mb-5">{warning(deletion)}</p>
          <div class="flex gap-2">
            <button
              class="flex-1 min-h-[44px] px-5 rounded-xl bg-danger text-white font-semibold active:scale-95 transition-transform"
              data-testid="note-confirm-yes"
              onClick={onConfirm}
            >
              {choose(
                folder,
                plural(deletion.count, "Delete 1 note", "Delete {n} notes"),
                t("Delete the note"),
              )}
            </button>
            <button
              class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
              data-testid="note-confirm-no"
              onClick={onCancel}
            >
              {t("Keep it")}
            </button>
          </div>
        </div>
      </ModalBackdrop>
    </BodyPortal>
  );
}
