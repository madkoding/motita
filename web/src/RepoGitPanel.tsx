// The git panel of a repository inside a project's folder: what it holds right now, and the
// things a person does by hand. Nothing here forces git; discarding asks twice.
import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { IconClose } from "./icons";
import type { RepoRef } from "./useRepoGit";
import type { RepoStatus } from "./appTypes";

const FIELD =
  "flex-1 min-w-0 px-3 py-2 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] text-sm focus:outline-hidden focus:border-accent";
const BTN =
  "px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 disabled:opacity-30 disabled:cursor-not-allowed";
const HEAD = "text-xs uppercase tracking-wide text-[#7a7a8c] mb-1.5";
const SHOWN = 30;

// Distance says how far the branch is from the main branch, and what exists only here.
function Distance({ s }: { s: RepoStatus }) {
  return (
    <p class="text-xs text-[#9a9aaa]" data-testid="repo-distance">
      {tf("{ahead} ahead, {behind} behind {main}", {
        ahead: s.ahead,
        behind: s.behind,
        main: s.main_branch ?? "main",
      })}
      {" · "}
      {tf("{n} not published", { n: s.unpublished })}
    </p>
  );
}

function ChangeList({ s }: { s: RepoStatus }) {
  return (
    <div>
      <h3 class={HEAD}>{tf("Changes ({n})", { n: s.changes.length })}</h3>
      {s.changes.length === 0 && (
        <p class="text-xs text-[#7a7a8c] italic">{t("Nothing changed")}</p>
      )}
      <ul class="font-mono text-xs space-y-0.5" data-testid="repo-changes">
        {s.changes.slice(0, SHOWN).map((c) => (
          <li key={c.path} class="flex gap-2">
            <span class="text-[#7a7a8c] flex-none w-[4rem] truncate">
              {c.kind}
            </span>
            <span class="truncate">{c.path}</span>
          </li>
        ))}
      </ul>
      {s.changes.length > SHOWN && (
        <p class="text-xs text-[#7a7a8c]">
          {tf("and {n} more", { n: s.changes.length - SHOWN })}
        </p>
      )}
    </div>
  );
}

function History({ s }: { s: RepoStatus }) {
  return (
    <div>
      <h3 class={HEAD}>{t("History")}</h3>
      <ul class="text-xs space-y-0.5" data-testid="repo-log">
        {s.log.map((c) => (
          <li key={c.sha} class="flex gap-2">
            <span class="font-mono text-[#7a7a8c] flex-none">{c.sha}</span>
            <span class="truncate">{c.subject}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

// Tools are the fields and buttons: a new branch, the changes put aside, the merged branches
// deleted, and the changes discarded after a second click.
function Tools({ r, s }: { r: RepoRef; s: RepoStatus }) {
  const { act } = useApp().repoGit;
  const [name, setName] = useState("");
  const [sure, setSure] = useState(false);
  return (
    <div class="space-y-3">
      <div class="flex gap-2">
        <input
          class={FIELD}
          data-testid="repo-branch-name"
          placeholder={t("New branch name")}
          value={name}
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
        />
        <button
          class={BTN}
          data-testid="repo-branch-create"
          disabled={!name.trim()}
          onClick={() => void act(r, "branch", { branch: name.trim() })}
        >
          {t("Create branch")}
        </button>
      </div>
      <div class="flex flex-wrap gap-2">
        <button
          class={BTN}
          data-testid="panel-stash"
          disabled={s.changes.length === 0}
          onClick={() => void act(r, "stash")}
        >
          {t("Put changes aside")}
        </button>
        <button
          class={BTN}
          data-testid="panel-clean"
          disabled={s.merged.length === 0}
          title={s.merged.join(", ")}
          onClick={() => void act(r, "clean")}
        >
          {tf("Delete merged branches ({n})", { n: s.merged.length })}
        </button>
        {!sure && (
          <button
            class={BTN + " text-danger"}
            data-testid="repo-discard"
            disabled={s.changes.length === 0}
            onClick={() => setSure(true)}
          >
            {t("Discard changes…")}
          </button>
        )}
      </div>
      {sure && (
        <div
          class="rounded-xl border border-danger/40 p-3 text-xs space-y-2"
          data-testid="repo-discard-sure"
        >
          <p>{t("This deletes the uncommitted work and cannot be undone.")}</p>
          <div class="flex gap-2">
            <button
              class={BTN + " text-danger"}
              data-testid="repo-discard-yes"
              onClick={() => {
                setSure(false);
                void act(r, "discard", { confirm: true });
              }}
            >
              {t("Discard")}
            </button>
            <button class={BTN} onClick={() => setSure(false)}>
              {t("Cancel")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

export function RepoGitPanel({ r }: { r: RepoRef }) {
  const { status, closeRepoGit } = useApp().repoGit;
  const s = status;
  return (
    <ModalBackdrop onClose={closeRepoGit}>
      <dialog
        open
        class="app-dialog frosted rounded-2xl border border-white/10 w-full max-w-lg p-5 shadow-2xl max-h-[92vh] overflow-y-auto"
        aria-label={r.repo}
      >
        <div class="flex items-center gap-2 mb-3">
          <h2 class="text-base font-semibold truncate">{r.repo}</h2>
          {s && (
            <span class="font-mono text-xs text-[#9a9aaa] truncate">
              {s.branch}
            </span>
          )}
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeRepoGit}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        {!s && <p class="text-sm text-[#7a7a8c]">{t("Reading…")}</p>}
        {s && (
          <div class="space-y-4">
            <Distance s={s} />
            <ChangeList s={s} />
            <History s={s} />
            <Tools r={r} s={s} />
          </div>
        )}
      </dialog>
    </ModalBackdrop>
  );
}
