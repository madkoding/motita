// The open pull requests of every project at a glance: what each one's CI says, whether it would
// merge, and which session follows it, so a failing CI or a stale branch shows without opening
// each session.
import { useCallback, useEffect, useState } from "preact/hooks";
import { PR_LIST_POLL_MS } from "./appConst";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { errorMessage } from "./errors";
import type { Api } from "./prApi";
import { mergeBlockedLabel } from "./appFormat";
import { ModalBackdrop } from "./ModalBackdrop";
import { IconClose, IconPullRequest, IconRefresh, IconSpinner } from "./icons";
import {
  closePullRequests,
  reloadPullRequests,
  usePullRequestsOpen,
  usePullRequestsReloads,
} from "./pullRequestsOpen";
import {
  canUpdate,
  ciBadge,
  failingFirst,
  fetchPullRequests,
  mergeNote,
  updatePRBranch,
  type PRItem,
  type PRList,
  type Tone,
} from "./pullRequestsApi";

const TONE: Record<Tone, string> = {
  good: "text-[#40f0a0] border-[#40f0a0]/30",
  bad: "text-danger border-danger/30",
  wait: "text-warning border-warning/30",
  idle: "text-[#9a9aaa] border-white/10",
};

const TAG = "px-2 py-0.5 rounded-full border text-xs whitespace-nowrap";

// ciWord is the CI state in words.
function ciWord(state: string): string {
  if (state === "success") {
    return t("passing");
  }
  if (state === "failure") {
    return t("failing");
  }
  if (state === "pending") {
    return t("running");
  }
  return t("no checks");
}

const itemKey = (i: PRItem) => i.repo + "#" + i.pr.number;

function Row({ item }: { item: PRItem }) {
  const { api } = useApp();
  const [updating, setUpdating] = useState(false);
  const [failure, setFailure] = useState("");
  const update = async () => {
    setUpdating(true);
    try {
      await updatePRBranch(api as Api, item);
      setFailure("");
      reloadPullRequests();
    } catch (e) {
      setFailure(errorMessage(e));
    }
    setUpdating(false);
  };
  const ci = ciBadge(item);
  const note = mergeNote(item);
  return (
    <li class="rounded-xl border border-white/10 p-3" data-testid="pr-row">
      <div class="flex items-center gap-2">
        <a
          class="flex-1 min-w-0 truncate text-sm text-[#e8e8ea] hover:underline"
          href={item.pr.url}
          target="_blank"
          rel="noopener noreferrer"
        >
          #{item.pr.number} {item.pr.title}
        </a>
        <span
          class={TAG + " " + TONE[ci.tone]}
          data-testid="pr-ci"
          title={t("CI")}
        >
          {ciWord(ci.label)}
          {ci.detail && " " + ci.detail}
        </span>
      </div>
      <p class="text-xs text-[#7a7a8c] mt-1 truncate">
        {item.project} · {item.repo}
        {item.pr.head && " · " + item.pr.head + " → " + (item.pr.base ?? "")}
      </p>
      {failure && (
        <p class="text-xs text-danger mt-1" data-testid="pr-update-error">
          {failure}
        </p>
      )}
      <div class="flex flex-wrap gap-1.5 mt-2 empty:hidden">
        {note && (
          <span class={TAG + " " + TONE.wait} data-testid="pr-merge">
            {mergeBlockedLabel(note)}
          </span>
        )}
        {canUpdate(item) && (
          <button
            class={
              TAG + " " + TONE.idle + " hover:bg-white/5 disabled:opacity-50"
            }
            data-testid="pr-update"
            disabled={updating}
            onClick={() => void update()}
          >
            {choose(updating, t("Updating…"), t("Update branch"))}
          </button>
        )}
        {item.session && (
          <span class={TAG + " " + TONE.idle} data-testid="pr-follow">
            {choose(
              !!item.watch,
              tf("following: {s}", { s: item.watch?.status ?? "" }),
              t("has a session"),
            )}
          </span>
        )}
      </div>
    </li>
  );
}

function Body({ list }: { list: PRList }) {
  const items = failingFirst(list.pull_requests);
  return (
    <>
      {items.length === 0 && (
        <p class="text-sm text-[#9a9aaa]" data-testid="pr-empty">
          {t("No open pull requests.")}
        </p>
      )}
      <ul class="space-y-2">
        {items.map((i) => (
          <Row key={itemKey(i)} item={i} />
        ))}
      </ul>
      {(list.problems ?? []).map((p) => (
        <p
          key={p.project}
          class="text-xs text-warning mt-2"
          data-testid="pr-problem"
        >
          {p.project}: {p.error}
        </p>
      ))}
    </>
  );
}

export function PullRequestsModal() {
  const open = usePullRequestsOpen();
  const { api } = useApp();
  const [list, setList] = useState<PRList | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const reloads = usePullRequestsReloads();
  const load = useCallback(
    async (refresh: boolean) => {
      if (!api) {
        return;
      }
      setBusy(true);
      try {
        setList(await fetchPullRequests(api, refresh));
        setError("");
      } catch (e) {
        setError(errorMessage(e));
      } finally {
        setBusy(false);
      }
    },
    [api],
  );
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    void load(false);
    const id = setInterval(() => void load(false), PR_LIST_POLL_MS);
    return () => clearInterval(id);
  }, [open, load]);
  useEffect(() => {
    if (open && reloads > 0) {
      void load(true);
    }
  }, [reloads]);
  if (!open) {
    return null;
  }
  return (
    <ModalBackdrop onClose={closePullRequests}>
      <div
        class="frosted rounded-2xl border border-white/10 w-full max-w-lg p-5 shadow-2xl max-h-[85vh] flex flex-col"
        data-testid="pull-requests"
      >
        <div class="flex items-center gap-2 mb-3">
          <IconPullRequest size={18} />
          <h2 class="text-base font-semibold">{t("Pull requests")}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5 disabled:opacity-40"
            data-testid="pr-refresh"
            disabled={busy}
            onClick={() => void load(true)}
            aria-label={t("Refresh")}
          >
            {choose(
              busy,
              <IconSpinner class="animate-spin" size={18} />,
              <IconRefresh size={18} />,
            )}
          </button>
          <button
            class="p-1.5 rounded-lg hover:bg-white/5"
            onClick={closePullRequests}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="overflow-y-auto min-h-0">
          {error && (
            <p class="text-sm text-danger" data-testid="pr-error">
              {error}
            </p>
          )}
          {list && <Body list={list} />}
        </div>
      </div>
    </ModalBackdrop>
  );
}
