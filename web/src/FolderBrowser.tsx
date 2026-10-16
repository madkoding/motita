// A mini folder browser: walk the folders of the machine and pick one for a project. It lists
// folders only; which part of the disk it reaches is the gateway's call (Settings → Security).
import { useEffect, useRef, useState } from "preact/hooks";
import { t } from "./i18n";
import { errorMessage } from "./errors";
import { useDialog } from "./useDialog";
import { useBackdropClose } from "./useBackdropClose";
import { useLeave } from "./motion";
import { listDirs, makeDir, type DirListing } from "./fsApi";
import { IconClose, IconFolder, IconSpinner } from "./icons";
import type { Api } from "./gitApi";
import "./dialog.css";

interface Props {
  api: Api;
  // The folder it opens on; the user's home (or the workspace) when empty.
  start?: string;
  onPick: (path: string) => void;
  onClose: () => void;
}

const ROW =
  "w-full flex items-center gap-2.5 px-3 min-h-[44px] rounded-xl text-left text-sm text-[#e8e8ea] hover:bg-white/5 active:bg-white/10";
const SMALL_BTN =
  "px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 disabled:opacity-30 disabled:cursor-not-allowed";

// Folders lists the sub-folders of the folder on show.
function Folders({
  listing,
  onOpen,
}: {
  listing: DirListing;
  onOpen: (path: string) => void;
}) {
  if (listing.dirs.length === 0) {
    return (
      <p class="px-3 py-6 text-center text-sm text-[#7a7a8c]">
        {t("No folders in here")}
      </p>
    );
  }
  return (
    <ul class="space-y-0.5" data-testid="folder-list">
      {listing.dirs.map((d) => (
        <li key={d.path}>
          <button
            type="button"
            class={ROW}
            data-testid="folder-row"
            onClick={() => onOpen(d.path)}
          >
            <IconFolder size={18} class="flex-none text-accent" />
            <span class="min-w-0 flex-1 truncate font-mono">{d.name}</span>
            {d.git && (
              <span class="flex-none text-[11px] px-1.5 py-0.5 rounded-md bg-white/10 text-[#9a9aaa]">
                git
              </span>
            )}
          </button>
        </li>
      ))}
    </ul>
  );
}

// NewFolder is the row that makes a folder inside the one on show.
function NewFolder({ onCreate }: { onCreate: (name: string) => void }) {
  const [name, setName] = useState("");
  const create = () => {
    if (name.trim()) {
      onCreate(name.trim());
      setName("");
    }
  };
  return (
    <div class="flex gap-2 mt-2">
      <input
        class="flex-1 min-w-0 px-3 py-2 rounded-xl bg-black/30 border border-white/10 text-sm font-mono text-[#e8e8ea] focus:outline-hidden focus:border-accent"
        data-testid="folder-new-name"
        value={name}
        placeholder={t("New folder name")}
        aria-label={t("New folder name")}
        onInput={(e) => setName((e.target as HTMLInputElement).value)}
      />
      <button
        type="button"
        class={SMALL_BTN}
        data-testid="folder-new"
        disabled={!name.trim()}
        onClick={create}
      >
        {t("Create")}
      </button>
    </div>
  );
}

// Navigation shows the folder on show, with the ways to leave it: up, and home.
function Navigation({
  listing,
  loading,
  onOpen,
}: {
  listing: DirListing | null;
  loading: boolean;
  onOpen: (path: string) => unknown;
}) {
  const parent = listing?.parent ?? "";
  const home = listing?.home ?? "";
  function up() {
    onOpen(parent);
  }
  function goHome() {
    onOpen(home);
  }
  return (
    <>
      <p
        class="mb-2 px-3 py-2 rounded-xl bg-black/30 border border-white/10 font-mono text-xs text-[#e8e8ea] break-all"
        data-testid="folder-path"
      >
        {listing?.path ?? "…"}
      </p>
      <div class="flex gap-2 mb-2">
        <button
          type="button"
          class={SMALL_BTN}
          data-testid="folder-up"
          disabled={!parent || loading}
          onClick={up}
        >
          {t("Up")}
        </button>
        {home && home !== listing?.path && (
          <button
            type="button"
            class={SMALL_BTN}
            data-testid="folder-home"
            disabled={loading}
            onClick={goHome}
          >
            {t("Home")}
          </button>
        )}
        {loading && (
          <IconSpinner class="animate-spin self-center text-accent" size={16} />
        )}
      </div>
    </>
  );
}

export function FolderBrowser({ api, start = "", onPick, onClose }: Props) {
  const ref = useRef<HTMLDialogElement>(null);
  const backdrop = useRef<HTMLDivElement>(null);
  useLeave(backdrop, "fade", "pop");
  useDialog(ref, onClose);
  useBackdropClose(backdrop, onClose);
  const [listing, setListing] = useState<DirListing | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState("");
  const req = useRef(0);

  async function open(path: string) {
    req.current += 1;
    const my = req.current;
    setLoading(true);
    setErr("");
    try {
      const l = await listDirs(api, path);
      if (my === req.current) {
        setListing(l);
      }
    } catch (e) {
      if (my === req.current) {
        setErr(errorMessage(e));
      }
    }
    if (my === req.current) {
      setLoading(false);
    }
  }
  async function create(name: string) {
    try {
      const made = await makeDir(api, (listing as DirListing).path, name);
      await open(made.path);
    } catch (e) {
      setErr(errorMessage(e));
    }
  }
  useEffect(() => {
    void open(start);
    return () => {
      req.current++;
    };
  }, []);

  const pick = () => {
    if (listing) {
      onPick(listing.path);
    }
  };
  return (
    <div
      ref={backdrop}
      class="fixed inset-0 z-110 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4"
      data-testid="folder-browser"
      data-modal
    >
      <dialog
        open
        ref={ref}
        data-dialog-root
        class="modal-dialog frosted rounded-2xl border border-white/10 w-full max-w-md max-h-[85vh] flex flex-col p-5 shadow-2xl"
        aria-modal="true"
        aria-label={t("Choose a folder")}
      >
        <div class="flex items-center gap-2 mb-3">
          <IconFolder size={20} class="text-accent" />
          <h2 class="flex-1 text-base font-semibold text-[#e8e8ea]">
            {t("Choose a folder")}
          </h2>
          <button
            class="p-1.5 rounded-lg hover:bg-white/5"
            aria-label={t("Close")}
            onClick={onClose}
          >
            <IconClose size={18} />
          </button>
        </div>
        <Navigation listing={listing} loading={loading} onOpen={open} />
        <div class="flex-1 min-h-[160px] overflow-y-auto rounded-xl border border-white/10 bg-black/20 p-1">
          {err && (
            <p class="px-3 py-4 text-sm text-danger" role="alert">
              {err}
            </p>
          )}
          {!err && listing && <Folders listing={listing} onOpen={open} />}
        </div>
        {listing?.sensitive && (
          <p
            class="mt-2 text-xs text-warning"
            role="note"
            data-testid="folder-sensitive"
          >
            {t(
              "This folder holds the system's own files. A project here puts the agent among them.",
            )}
          </p>
        )}
        {listing && <NewFolder onCreate={create} />}
        <div class="flex gap-2 mt-4">
          <button
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
            data-testid="folder-pick"
            disabled={!listing || !!err}
            onClick={pick}
          >
            {t("Use this folder")}
          </button>
          <button
            class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
            onClick={onClose}
          >
            {t("Cancel")}
          </button>
        </div>
      </dialog>
    </div>
  );
}
