import { createContext } from "preact";
import {
  useState,
  useEffect,
  useRef,
  useCallback,
  useContext,
} from "preact/hooks";
import { Markdown } from "./Markdown";
import { Lightbox } from "./Lightbox";
import { ReportCard } from "./Report";
import type { SuggestionAnswers } from "./proposals";
import "./report.css";
import { copyText } from "./clipboard";
import { t, tf, useLang } from "./i18n";
import { GitConnectModal } from "./GitConnect";
import { RepoPicker } from "./RepoPicker";
import { connectedAccounts } from "./gitApi";
import { loadNotify } from "./settings";
import {
  getPR,
  retryFix,
  mergePR,
  mergeRefused,
  PRError,
  prNotice,
  shouldPoll,
  ciSummary,
  shouldDesktopNotify,
  wantsDesktopPermission,
  worstToast,
  type Notice,
  type PRView,
  type PRWatch,
  type ToastType,
} from "./prApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import { clearCredential } from "./appAuth";
import { AppCtx, useApp, useAppStore, type Store } from "./appStore";
import {
  ciFailedLabel,
  mergeBlockedLabel,
  mergedOffer,
  prWatchKeyOf,
} from "./appFormat";
import {
  AUTH_NEEDED_EVENT,
  COPIED_MS,
  HTTP_UNAUTHORIZED,
  NEW_SESSION_TITLE,
  PR_POLL_MS,
  SAME_ORIGIN,
  SESSIONS_PATH,
} from "./appConst";
import type { Message, SessionInfo, StepView } from "./appTypes";
import { AuthModal } from "./AuthModal";
import { Sidebar } from "./Sidebar";
import { ChatHeader } from "./ChatHeader";
import { AgentsDrawer, TermDrawer, ThinkDrawer } from "./Drawers";
import { ApprovalPanel, ChatLoadingModal } from "./ChatPanels";
import { Composer } from "./Composer";
import { NewSessionButton } from "./NewSessionButton";
import {
  ActivityLine,
  ChangesCard,
  RunArtifacts,
  Trail,
  TurnHead,
  UserText,
} from "./ConversationParts";
import { EditProjectModal } from "./EditProjectModal";
import { NewProjectModal } from "./NewProjectModal";
import { GitIdentityModal } from "./GitIdentityModal";
import { ModelSwitcherModal } from "./ModelSwitcherModal";
import { ConfirmDeleteModal, ContextMenu } from "./ConfirmDeleteModal";
import { ToastCard } from "./ToastCard";
import { UpgradeModal } from "./UpgradeModal";
import { SkillLibraryModal } from "./SkillLibraryModal";
import { ArtifactsModal } from "./ArtifactsModal";
import { ScheduledTasksModal } from "./ScheduledTasksModal";

// api is the one place a request is built. The cookie is sent automatically
// by the browser; the X-Motita header is what tells the gateway a request was
// built by this page and not by another page on the same host (the cookie is
// not isolated by port), so every request carries it.
//
// On 401, the credential is stale: the cookie is cleared and an event is
// dispatched so the blocking auth modal reappears.
async function api(path: string, options?: RequestInit): Promise<Response> {
  const headers = new Headers(options?.headers);
  headers.set("X-Motita", "1");
  const res = await fetch(path, {
    credentials: SAME_ORIGIN,
    ...options,
    headers,
  });
  if (res.status === HTTP_UNAUTHORIZED) {
    await clearCredential();
    window.dispatchEvent(new CustomEvent(AUTH_NEEDED_EVENT));
    throw new Error("unauthorised");
  }
  return res;
}

// EvidenceCtx hands the report cards the loader of the screenshots they name.
type EvidenceLoader = (name: string) => Promise<string | null>;
const EvidenceCtx = createContext<EvidenceLoader>(() => Promise.resolve(null));

// useEvidenceLoader is loadEvidenceImage: it fetches a screenshot a report names, from this
// session's artifacts, as an object URL the card can show. The same file is fetched once, however
// many cards ask.
function useEvidenceLoader(store: Store): EvidenceLoader {
  const { sessionId } = store.rs;
  const { artifactBase } = store.artifacts;
  const evidenceUrls = useRef(new Map<string, Promise<string | null>>());
  const loadEvidenceImage = useCallback(
    (name: string) => {
      const key = sessionId + "/" + name;
      let p = evidenceUrls.current.get(key);
      if (!p) {
        p = api(artifactBase() + "/" + encodeURIComponent(name))
          .then(async (res) => {
            if (!res.ok) {
              return null;
            }
            return URL.createObjectURL(await res.blob());
          })
          .catch(() => null);
        evidenceUrls.current.set(key, p);
      }
      return p;
    },
    [sessionId, artifactBase],
  );
  return loadEvidenceImage;
}

// ── Pull request bar ─────────────────────────────────────────────────────
// A session with work in it can propose it: "Create PR" has the agent commit, push and open
// the pull request. From then on the GATEWAY follows its CI (sending the agent to fix a failure,
// push after push, up to a limit) and reports it in the session list; this side draws the bar
// and turns each step of that into a toast, for any session and not only the one on screen.

interface PRToast {
  message: string;
  type: ToastType;
  detail: string;
}

// noticeToast is the toast a step of the gateway's watch is told with, or null for a step that
// is not worth one. `where` names the session when it is not the one on screen.
function noticeToast(
  notice: Notice,
  cur: PRWatch,
  where: string,
): PRToast | null {
  const pr = choose(cur.number, "#" + cur.number, "");
  const n = String(cur.attempts);
  switch (notice) {
    case "passed":
      return {
        message: t("CI passed") + where,
        type: "success",
        detail: tf("Pull request {pr} is green: you can go and merge it.", {
          pr,
        }),
      };
    case "fixing":
      return {
        message: t("CI failed") + where,
        type: "error",
        detail: tf("Motita is fixing it — attempt {n} of {max}.", {
          n,
          max: String(cur.max),
        }),
      };
    case "reviewing":
      return {
        message: t("New review comments") + where,
        type: "warning",
        detail: tf(
          "Motita is answering the comments on pull request {pr} — attempt {n} of {max}.",
          { pr, n, max: String(cur.max) },
        ),
      };
    case "resolving":
      return {
        message: t("Merge conflict") + where,
        type: "warning",
        detail: tf(
          "Pull request {pr} conflicts with its base branch: Motita is resolving it — attempt {n} of {max}.",
          { pr, n, max: String(cur.max) },
        ),
      };
    case "gave_up":
      return {
        message: t("Motita could not fix the CI") + where,
        type: "error",
        detail: tf(
          "It tried {n} times. Look at pull request {pr} and try again when you are ready.",
          { n, pr },
        ),
      };
    case "no_ci":
      return {
        message: t("This repository has no CI") + where,
        type: "warning",
        detail: tf("Pull request {pr} has no checks to wait for.", { pr }),
      };
    case "base_red":
      return {
        message: t("The CI is red on the base branch too") + where,
        type: "warning",
        detail: tf(
          "Pull request {pr} fails the same checks as the branch it merges into: this change did not cause it.",
          { pr },
        ),
      };
    case "closed":
      return {
        message: t("Pull request closed") + where,
        type: "warning",
        detail: tf("Pull request {pr} was closed without being merged.", {
          pr,
        }),
      };
    case "merged":
      return {
        message: t("Pull request merged") + where,
        type: "success",
        detail: choose(
          cur.next,
          tf(
            "Pull request {pr} was merged. A new session was opened from the updated branch.",
            { pr },
          ),
          tf(
            "Pull request {pr} was merged. Use Continue in the bar to keep working from the updated branch.",
            { pr },
          ),
        ),
      };
    default:
      return null;
  }
}

// announce shows the toast and, when the tab is in the background, a browser notification as well:
// the CI ending is exactly what someone looking at another window wants to hear about.
function announce(
  setToast: Store["status"]["setToast"],
  n: { message: string; type: ToastType; detail?: string },
) {
  setToast(n);
  try {
    if (
      typeof Notification !== "undefined" &&
      shouldDesktopNotify(
        document.hidden,
        Notification.permission,
        loadNotify(),
      )
    ) {
      new Notification(n.message, { body: n.detail, tag: "motita-pr" });
    }
  } catch {
    /* a browser that cannot notify just does not */
  }
}

// announceAll tells what moved in one look at the list as one toast: three sessions finishing
// together are one thing to read, not three toasts replacing each other.
function announceAll(setToast: Store["status"]["setToast"], news: PRToast[]) {
  if (news.length === 1) {
    announce(setToast, news[0]);
    return;
  }
  if (news.length === 0) {
    return;
  }
  announce(setToast, {
    message: tf("{n} pull requests moved", { n: String(news.length) }),
    type: worstToast(news.map((n) => n.type)),
    detail: news.map((n) => n.message).join(" · "),
  });
}

// askDesktopPermission asks, on the click that creates a pull request, to be allowed to say the
// CI ended while the tab is in the background.
function askDesktopPermission() {
  try {
    if (
      typeof Notification !== "undefined" &&
      wantsDesktopPermission(Notification.permission)
    ) {
      void Notification.requestPermission();
    }
  } catch {
    /* ignore */
  }
}

// usePRToasts reads the list for every session, so a CI that ends while another one is open is
// told too, with the session's name.
function usePRToasts(store: Store) {
  const { sessions } = store.lists;
  const { sessionRef } = store.rs;
  const { setToast } = store.status;
  const seenWatchRef = useRef<Record<string, PRWatch | null>>({});
  useEffect(() => {
    const news: PRToast[] = [];
    for (const sess of sessions) {
      const cur = sess.pr_watch ?? null;
      const notice = prNotice(seenWatchRef.current[sess.id], cur);
      seenWatchRef.current[sess.id] = cur;
      if (!notice || !cur) {
        continue;
      }
      const where = choose(
        sess.id === sessionRef.current,
        "",
        " — " + (sess.title || sess.id),
      );
      const toast = noticeToast(notice, cur, where);
      if (toast) {
        news.push(toast);
      }
    }
    announceAll(setToast, news);
  }, [sessions]);
}

function usePullRequest(store: Store) {
  const { rs, lists, stream, projects, status, selectedSession } = store;
  const { sessionId, sessionRef } = rs;
  const { setToast } = status;
  const [prView, setPrView] = useState<PRView | null>(null);
  // The reason the host cannot be asked (no remote, a host motita does not speak): the bar is
  // not drawn rather than offering something that cannot work.
  const [prBlocked, setPrBlocked] = useState(false);
  const [prMergeArmed, setPrMergeArmed] = useState(false);
  const [prBusy, setPrBusy] = useState(false);
  const prSessionId = choose(
    selectedSession?.project_id && !selectedSession.merged,
    sessionId,
    "",
  );
  const prRunning = selectedSession?.running === true;
  const prWatch = selectedSession?.pr_watch;
  const prWatchKey = prWatchKeyOf(prWatch);

  const refreshPR = async (sid: string) => {
    try {
      const v = await getPR(api, sid);
      if (sid !== sessionRef.current) {
        return;
      }
      setPrBlocked(false);
      setPrView(v);
    } catch (e) {
      if (sid !== sessionRef.current) {
        return;
      }
      // 'pr_no_remote' and 'pr_no_host' will not change by asking again; a lost connection or a
      // host that is down will, and the next poll tries again.
      if (
        e instanceof PRError &&
        (e.code === "pr_no_remote" || e.code === "pr_no_host")
      ) {
        setPrBlocked(true);
      }
    }
  };

  // The session on screen is asked when it is opened, when its run ends (the agent has just
  // pushed or opened the pull request) and whenever the gateway's watch moves.
  useEffect(() => {
    setPrView(null);
    setPrBlocked(false);
    setPrMergeArmed(false);
  }, [prSessionId]);
  const wasRunningRef = useRef(false);
  useEffect(() => {
    if (!prSessionId) {
      return;
    }
    const ended = wasRunningRef.current && !prRunning;
    wasRunningRef.current = prRunning;
    if (ended || prView === null) {
      void refreshPR(prSessionId);
    }
  }, [prSessionId, prRunning]);
  useEffect(() => {
    if (prSessionId && prWatchKey) {
      void refreshPR(prSessionId);
    }
  }, [prSessionId, prWatchKey]);
  // While the gateway follows the CI, the bar's detail (the jobs) is refreshed every 20 seconds.
  useEffect(() => {
    if (!prSessionId || !shouldPoll(prWatch)) {
      return undefined;
    }
    const id = setInterval(() => {
      void refreshPR(prSessionId);
    }, PR_POLL_MS);
    return () => clearInterval(id);
  }, [prSessionId, prWatch?.status]);

  usePRToasts(store);

  const createPR = async () => {
    const sid = sessionRef.current;
    askDesktopPermission();
    // Asked before the agent is, so a host that is not connected is a dialog and not a turn spent.
    try {
      await getPR(api, sid);
    } catch (e) {
      if (e instanceof PRError && e.code === "git_auth_required") {
        projects.setGitConnect({ service: e.service || undefined });
        return;
      }
      setToast({
        message: t("Could not create the pull request"),
        type: "error",
        detail: errorMessage(e),
      });
      return;
    }
    await stream.submit(
      t("Create PR"),
      SESSIONS_PATH + encodeURIComponent(sid) + "/pr",
    );
  };

  // tryAgain is what the user does after the gateway gave up: the agent is sent again and the
  // count starts over.
  const tryAgain = async () => {
    const sid = sessionRef.current;
    setPrBusy(true);
    try {
      const { started } = await retryFix(api, sid);
      if (started && sid === sessionRef.current) {
        // The run is one this tab did not start: attach to it like a reconnection does.
        rs.runSessionRef.current = sid;
        rs.lastIdRef.current = 0;
        lists.setRunningState(true);
        status.setState("working");
        void stream.followReconnect();
      } else {
        rs.handoffRef.current = true;
      }
    } catch (e) {
      setToast({
        message: t("Could not ask Motita to fix the CI"),
        type: "error",
        detail: errorMessage(e),
      });
    } finally {
      setPrBusy(false);
    }
  };

  // mergeNow asks twice: merging is the one step here that cannot be taken back.
  const mergeNow = async () => {
    if (!prMergeArmed) {
      setPrMergeArmed(true);
      return;
    }
    const sid = sessionRef.current;
    setPrMergeArmed(false);
    setPrBusy(true);
    try {
      await mergePR(api, sid);
      void refreshPR(sid);
      void lists.fetchSessions();
    } catch (e) {
      setToast({
        message: t("Could not merge the pull request"),
        type: "error",
        detail: errorMessage(e),
      });
    } finally {
      setPrBusy(false);
    }
  };

  const disarmMerge = useCallback(() => setPrMergeArmed(false), []);

  return {
    prView,
    prBlocked,
    prMergeArmed,
    prBusy,
    prSessionId,
    prRunning,
    prWatch,
    createPR,
    tryAgain,
    mergeNow,
    disarmMerge,
  };
}

type PullRequest = ReturnType<typeof usePullRequest>;

// showsBar says whether the bar is drawn: for a session of a project (or one whose pull request
// merged), when the host can be asked, and when there is something to show or to propose.
function showsBar(pr: PullRequest, s: SessionInfo | undefined): boolean {
  const merged = pr.prWatch?.status === "merged";
  if (!pr.prSessionId && !(s?.merged && merged)) {
    return false;
  }
  if (pr.prBlocked) {
    return false;
  }
  return merged || pr.prView?.state === "open" || !!s?.mergeable;
}

const BAR_BUTTON = "px-3 min-h-[32px] rounded-lg font-semibold";
const ACCENT_BUTTON = `ml-auto ${BAR_BUTTON} bg-accent text-white active:scale-95 transition-transform`;

// MergedPart is the bar of a pull request that merged: the session to go on in.
function MergedPart({
  watch,
  offer,
  session,
}: {
  watch: PRWatch;
  offer: string;
  session: SessionInfo | undefined;
}) {
  const { conv } = useApp();
  return (
    <>
      <span class="inline-flex items-center gap-1.5 font-semibold text-[#a78bfa]">
        <span class="inline-block w-2 h-2 rounded-full bg-[#a78bfa]" />
        {tf("Pull request #{n} merged", { n: String(watch.number ?? "") })}
      </span>
      {offer === "open" && (
        <button
          type="button"
          class={ACCENT_BUTTON}
          title={t("A new session was opened from the updated branch")}
          onClick={() => {
            void conv.switchSession(watch.next as string);
          }}
        >
          {t("Open the new session")}
        </button>
      )}
      {!!session && offer === "continue" && (
        <ContinueButton session={session} />
      )}
    </>
  );
}

// ContinueButton opens a new session from the updated project branch.
function ContinueButton({ session }: { session: SessionInfo }) {
  const { actions } = useApp();
  return (
    <button
      type="button"
      class={ACCENT_BUTTON}
      title={t("Open a new session from the updated project branch")}
      onClick={() => {
        void actions.continueSession(session);
      }}
    >
      {t("Continue in a new session")}
    </button>
  );
}

// CiFailure is a red CI: what failed, and what Motita does about it.
function CiFailure({ pr, view }: { pr: PullRequest; view: PRView }) {
  const status = pr.prWatch?.status;
  return (
    <>
      <span
        class="inline-flex items-center gap-1.5 text-[#f87171]"
        title={(view.ci?.checks ?? [])
          .filter((c) => c.state === "failure")
          .map((c) => c.name)
          .join(", ")}
      >
        <span class="inline-block w-2 h-2 rounded-full bg-[#f87171]" />
        {ciFailedLabel(pr.prWatch)}
      </span>
      {status === "base_red" && (
        <span class="text-[#8a8a9a]">
          {t("Also failing on the base branch — not caused by this change")}
        </span>
      )}
      {status === "gave_up" && (
        <button
          type="button"
          class={`${BAR_BUTTON} border border-accent/40 text-accent disabled:opacity-50`}
          disabled={pr.prBusy || pr.prRunning}
          onClick={() => {
            void pr.tryAgain();
          }}
        >
          {t("Try again")}
        </button>
      )}
    </>
  );
}

// CiSuccess is a green CI: why the host would not merge it, or the merge itself, asked twice.
function CiSuccess({ pr, view }: { pr: PullRequest; view: PRView }) {
  const refused = mergeRefused(view);
  const blocked = !!view.merge && (refused || view.merge.code === "behind");
  return (
    <>
      <span class="inline-flex items-center gap-1.5 text-[#4ade80]">
        <span class="inline-block w-2 h-2 rounded-full bg-[#4ade80]" />
        {t("CI passed — ready to merge")}
      </span>
      {blocked && (
        <span class="ml-auto text-[#f0a040]">
          {mergeBlockedLabel(view.merge?.code ?? "")}
        </span>
      )}
      {!refused && (
        <button
          type="button"
          class={`ml-auto ${BAR_BUTTON} disabled:opacity-50 active:scale-95 transition-transform ${choose(pr.prMergeArmed, "bg-[#4ade80] text-black", "border border-[#4ade80]/50 text-[#4ade80]")}`}
          disabled={pr.prBusy}
          title={t("Merge this pull request on the host")}
          onBlur={pr.disarmMerge}
          onClick={() => {
            void pr.mergeNow();
          }}
        >
          {choose(pr.prMergeArmed, t("Click again to merge"), t("Merge"))}
        </button>
      )}
    </>
  );
}

// OpenPart is the bar of an open pull request: its link and its CI.
function OpenPart({
  pr,
  view,
  link,
}: {
  pr: PullRequest;
  view: PRView;
  link: NonNullable<PRView["pr"]>;
}) {
  const ci = view.ci?.state;
  const summary = ciSummary(view);
  return (
    <>
      <a
        href={link.url}
        target="_blank"
        rel="noopener noreferrer"
        class="font-semibold text-accent hover:underline"
        title={link.title || undefined}
      >
        {tf("Pull request #{n}", { n: String(link.number) })}
      </a>
      {ci === "pending" && (
        <span
          class="inline-flex items-center gap-1.5 text-[#f0a040]"
          title={t("The CI is running")}
        >
          <span class="inline-block w-2 h-2 rounded-full bg-[#f0a040] animate-pulse" />
          {tf("CI running {done}/{total}", {
            done: String(summary.passed),
            total: String(summary.total),
          })}
        </span>
      )}
      {ci === "success" && <CiSuccess pr={pr} view={view} />}
      {ci === "failure" && <CiFailure pr={pr} view={view} />}
      {ci === "none" && (
        <span class="text-[#8a8a9a]">{t("No CI checks yet")}</span>
      )}
    </>
  );
}

// CreateButton has the agent commit, push and open the pull request.
function CreateButton({ pr }: { pr: PullRequest }) {
  return (
    <button
      type="button"
      class="inline-flex items-center gap-1.5 px-3 min-h-[32px] rounded-lg bg-accent text-white font-semibold disabled:opacity-50 active:scale-95 transition-transform"
      disabled={pr.prRunning}
      title={choose(
        pr.prRunning,
        t("Wait for the current turn to end"),
        t("Commit, push and open a pull request with this session's work"),
      )}
      onClick={() => {
        void pr.createPR();
      }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <circle cx="6" cy="6" r="3" />
        <circle cx="6" cy="18" r="3" />
        <circle cx="18" cy="18" r="3" />
        <path d="M6 9v6" />
        <path d="M18 15V9a3 3 0 0 0-3-3h-3" />
        <path d="m14 3-3 3 3 3" />
      </svg>
      {t("Create PR")}
    </button>
  );
}

// openLinkOf is the pull request the bar links to, while it is open.
function openLinkOf(view: PRView | null) {
  if (view?.state !== "open") {
    return null;
  }
  return view.pr ?? null;
}

// PRBarBody is what the bar holds: the merged pull request and where to go on, the open one, or
// the button that creates it.
function PRBarBody({
  pr,
  session,
}: {
  pr: PullRequest;
  session: SessionInfo | undefined;
}) {
  const watch = pr.prWatch;
  if (watch?.status === "merged") {
    return (
      <MergedPart
        watch={watch}
        offer={mergedOffer(watch, session)}
        session={session}
      />
    );
  }
  const link = openLinkOf(pr.prView);
  if (link && pr.prView) {
    return <OpenPart pr={pr} view={pr.prView} link={link} />;
  }
  return <CreateButton pr={pr} />;
}

function PRBar({ pr }: { pr: PullRequest }) {
  const { selectedSession } = useApp();
  if (!showsBar(pr, selectedSession)) {
    return null;
  }
  return (
    <output class="flex items-center gap-2 px-3 sm:px-5 py-2 border-t border-white/5 text-xs flex-none z-10 relative">
      <PRBarBody pr={pr} session={selectedSession} />
    </output>
  );
}
// ── end of the pull request bar ──────────────────────────────────────────

// Group is one input with the steps it caused and the answer.
interface Group {
  key: number;
  turn?: number;
  items: Message[];
}

// groupsOf splits the conversation in one group per input. The input is sticky INSIDE its group,
// so it stays at the top while its own turn is on screen and hands over to the previous one when
// the reader scrolls back up past it.
function groupsOf(messages: Message[]): Group[] {
  const groups: Group[] = [];
  for (const m of messages) {
    if (m.role === "user" || groups.length === 0) {
      groups.push({ key: m.id, turn: m.turn, items: [m] });
    } else {
      groups[groups.length - 1].items.push(m);
    }
  }
  return groups;
}

// MessageBody is an answer of the agent (a report card or Markdown), or any other line as text.
function MessageBody({ m }: { m: Message }) {
  const loadEvidenceImage = useContext(EvidenceCtx);
  const { rs, answerSuggestions } = useApp();
  if (m.role !== "agent" || m.kind) {
    if (m.role === "user") {
      return <UserText m={m} />;
    }
    return <span>{choose(m.kind === "kind", t(m.text), m.text)}</span>;
  }
  if (m.report) {
    // The suggestions of a report are answered only while nothing runs: applying them is a turn.
    const onAnswer = choose(
      rs.running,
      undefined,
      (a: SuggestionAnswers) => void answerSuggestions(m, a),
    );
    // Suggestions left unanswered once the conversation moved on are stale: the work they were
    // about has changed since, so they are no longer offered.
    const expired =
      !m.answers && rs.messages.some((x) => x.role === "user" && x.id > m.id);
    return (
      <ReportCard
        report={m.report}
        loadImage={loadEvidenceImage}
        answers={m.answers}
        expired={expired}
        onAnswer={onAnswer}
      />
    );
  }
  return <Markdown content={m.text} />;
}

function MessageItem({ m }: { m: Message }) {
  const answer = m.role === "agent" && !m.kind;
  return (
    <div
      class={`msg ${m.role}${choose(m.kind, " " + m.kind, "")}`}
      data-who={choose(answer, t("Agent"), undefined)}
    >
      <MessageBody m={m} />
      {m.changes && <ChangesCard changes={m.changes} />}
      {m.artifacts && <RunArtifacts artifacts={m.artifacts} />}
    </div>
  );
}

// stepsOf are the steps a turn shows: thoughts live in the thinking drawer, the chat keeps steps
// and actions.
function stepsOf(
  stepsByTurn: Record<number, StepView[]>,
  turn?: number,
): StepView[] {
  if (turn == null) {
    return [];
  }
  return (stepsByTurn[turn] || []).filter((st) => st.kind !== "thought");
}

function Turn({ g, isLast }: { g: Group; isLast: boolean }) {
  const { running, activity, stepsByTurn } = useApp().rs;
  const head = choose(g.items[0].role === "user", g.items[0], null);
  const rest = choose(head, g.items.slice(1), g.items);
  const shown = stepsOf(stepsByTurn, g.turn);
  const liveHere = running && isLast;
  // The step in progress is the activity line below, so it is not drawn twice.
  const trail = choose(liveHere && activity, shown.slice(0, -1), shown);
  return (
    <div class="turn">
      {head && <TurnHead head={head} turn={g.turn} liveHere={liveHere} />}
      {trail.length > 0 && <Trail steps={trail} liveHere={liveHere} />}
      {rest.map((m) => (
        <MessageItem key={m.id} m={m} />
      ))}
      {liveHere && activity && (
        <ActivityLine activity={activity} shown={shown} />
      )}
    </div>
  );
}

// Conversation — the decorative gradient lives here only.
function Conversation() {
  const { rs, scroll } = useApp();
  const groups = groupsOf(rs.messages);
  return (
    <main
      ref={scroll.scrollRef}
      role="log"
      aria-live="polite"
      aria-label={t("conversation")}
      class={`chat-bg flex-1 overflow-y-auto px-3 py-4 sm:px-5 sm:py-5 flex flex-col gap-2.5${choose(
        scroll.chatLoading,
        " chat-loading",
        "",
      )}`}
      data-chat-loading={choose(scroll.chatLoading, "1", "0")}
    >
      {groups.map((g, gi) => (
        <Turn key={g.key} g={g} isLast={gi === groups.length - 1} />
      ))}
    </main>
  );
}

// flashCopied copies the text and, when it worked, shows a tick on the button for a moment.
function flashCopied(target: HTMLElement, text: string) {
  void copyText(text).then((ok) => {
    if (!ok) {
      return;
    }
    target.textContent = "✓";
    setTimeout(() => {
      target.textContent = t("copy");
    }, COPIED_MS);
  });
}

// previewOf is what the preview shows of a picture: its source and its name, when it has a source.
function previewOf(
  img: HTMLImageElement,
): { src: string; label: string } | null {
  const src = img.currentSrc || img.src;
  if (!src) {
    return null;
  }
  return { src, label: img.alt || t("Image") };
}

// copyTextOf is the text a copy button copies (a code block's or a whole message's), or null when
// the click was not on one.
function copyTextOf(target: HTMLElement): string | null {
  if (target.classList.contains("copy-btn")) {
    return target.dataset.copyText || "";
  }
  if (target.classList.contains("copy-msg-btn")) {
    return target.dataset.raw || "";
  }
  return null;
}

// ImagePreview: a picture inside an answer opens in the full-window preview (zoom, pan). A picture
// that is also a link keeps being a link. The same listener serves the copy buttons.
function ImagePreview() {
  const [previewImage, setPreviewImage] = useState<{
    src: string;
    label: string;
  } | null>(null);
  const closePreview = useCallback(() => setPreviewImage(null), []);

  // Copy button handler: delegate clicks from copy-btn and copy-msg-btn.
  useEffect(() => {
    const handler = (e: Event) => {
      const target = e.target as HTMLElement;
      if (
        target.tagName === "IMG" &&
        target.closest(".markdown-body") &&
        !target.closest("a")
      ) {
        const shown = previewOf(target as HTMLImageElement);
        if (shown) {
          setPreviewImage(shown);
        }
        return;
      }
      // Any other click is a copy, or not something this listener handles.
      const text = copyTextOf(target);
      if (text !== null) {
        flashCopied(target, text);
      }
    };
    document.addEventListener("click", handler);
    return () => document.removeEventListener("click", handler);
  }, []);

  if (!previewImage) {
    return null;
  }
  return <Lightbox images={[previewImage]} start={0} onClose={closePreview} />;
}

// NoSession is the chat when there is no conversation: none was created yet, or every one was
// deleted. Nothing is created behind the user's back; the chat waits, and says how to start one.
function NoSession() {
  const { actions } = useApp();
  const { createSession } = actions;
  const newSession = useCallback(() => createSession(), [createSession]);
  return (
    <main
      class="chat-bg flex-1 flex flex-col items-center justify-center gap-4 px-6 text-center"
      aria-live="polite"
      data-no-session="1"
    >
      <p class="text-base font-semibold">{t("Waiting for a new session")}</p>
      <p class="text-sm text-[#9a9aaa] max-w-sm">
        {t("Create a session to start talking to the agent.")}
      </p>
      <div class="w-full max-w-[220px] flex">
        <NewSessionButton
          loading={actions.creatingSession === ""}
          onClick={newSession}
          label={t(NEW_SESSION_TITLE)}
        />
      </div>
    </main>
  );
}

// MainColumn is the header, the conversation, the drawers and panels over it, and the composer.
// `relative` is what anchors the chat spinner to THIS column: without it the spinner would be
// positioned against the nearest positioned ancestor, or the window, and would cover the sidebar.
function MainColumn({ pr }: { pr: PullRequest }) {
  const { rs, scroll } = useApp();
  if (rs.noSession) {
    return (
      <div class="flex flex-col flex-1 min-w-0 h-dvh relative">
        <ChatHeader />
        <NoSession />
      </div>
    );
  }
  return (
    <div class="flex flex-col flex-1 min-w-0 h-dvh relative">
      <ChatHeader />
      <Conversation />
      <TermDrawer />
      <ThinkDrawer />
      <AgentsDrawer />
      {scroll.chatLoading && <ChatLoadingModal />}
      {rs.approval && <ApprovalPanel approval={rs.approval} />}
      <PRBar pr={pr} />
      <Composer />
    </div>
  );
}

// ProjectModals are the dialogs of a project: editing it, creating it, and the git host
// connections it can need.
function ProjectModals() {
  const { api: request, projects } = useApp();
  const {
    editProject,
    showNewProject,
    showGitIdentity,
    showRepoPicker,
    gitAccounts,
    gitConnect,
  } = projects;
  return (
    <>
      {editProject && <EditProjectModal p={editProject} />}
      {showNewProject && <NewProjectModal />}
      {showGitIdentity && <GitIdentityModal />}
      {showRepoPicker && gitAccounts && (
        <RepoPicker
          api={request}
          accounts={connectedAccounts(gitAccounts)}
          onPick={projects.pickRepo}
          onClose={projects.closeRepoPicker}
          onConnectAnother={projects.connectAnother}
          onAuthRequired={projects.pickerAuthRequired}
        />
      )}
      {gitConnect && (
        <GitConnectModal
          api={request}
          service={gitConnect.service}
          selfHosted={gitConnect.selfHosted}
          onClose={projects.closeGitConnect}
          onConnected={projects.onGitConnected}
        />
      )}
    </>
  );
}

// DialogModals are the model switcher, the delete confirmation, the long-press menu, the toast
// and the upgrade.
function DialogModals() {
  const { model, deletion, status, updates } = useApp();
  return (
    <>
      {model.showModelSwitcher && <ModelSwitcherModal />}
      {deletion.confirmDelete && (
        <ConfirmDeleteModal target={deletion.confirmDelete} />
      )}
      <ContextMenu />
      {status.toast && <ToastCard toast={status.toast} />}
      {updates.showUpgrade && <UpgradeModal />}
    </>
  );
}

// LibraryModals are the skill library, the image preview, the artifacts and the scheduled tasks.
function LibraryModals() {
  const { skills, artifacts, schedules } = useApp();
  return (
    <>
      {skills.showSkillLibrary && <SkillLibraryModal />}
      <ImagePreview />
      {artifacts.showArtifacts && <ArtifactsModal />}
      {schedules.showScheduledTasks && <ScheduledTasksModal />}
    </>
  );
}

export default function App() {
  // Every component under App re-renders when the language changes: App itself subscribes.
  useLang();
  const store = useAppStore(api);
  const pr = usePullRequest(store);
  const loadEvidenceImage = useEvidenceLoader(store);
  return (
    <AppCtx.Provider value={store}>
      <EvidenceCtx.Provider value={loadEvidenceImage}>
        <div class="app-bg flex h-dvh text-[#e8e8ea] overflow-hidden">
          {/* Blocking auth modal — shown when the browser has no valid credential.
              It covers the entire screen and cannot be dismissed without a token. */}
          {store.auth.authState !== "ok" && <AuthModal />}
          {/* Sidebar — sessions list, toggleable on all sizes. */}
          {store.lists.sidebarOpen && <Sidebar />}
          <MainColumn pr={pr} />
          <ProjectModals />
          <DialogModals />
          <LibraryModals />
        </div>
      </EvidenceCtx.Provider>
    </AppCtx.Provider>
  );
}
