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
import { t, useLang, type LangSetting } from "./i18n";
import { SettingsModal } from "./Settings";
import { GitConnectModal } from "./GitConnect";
import { RepoPicker } from "./RepoPicker";
import { connectedAccounts } from "./gitApi";
import { PRBar, usePullRequest, type PullRequest } from "./PullRequestBar";
import { choose } from "./choose";
import { modeOf } from "./appParse";
import { clearCredential } from "./appAuth";
import { AppCtx, useApp, useAppStore, type Store } from "./appStore";
import {
  AUTH_NEEDED_EVENT,
  COPIED_MS,
  HTTP_UNAUTHORIZED,
  NEW_SESSION_TITLE,
  NOTHING_YET,
  SAME_ORIGIN,
} from "./appConst";
import type { Message, StepView } from "./appTypes";
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
import { useVisualViewport } from "./viewport";

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
// `auto` says the turn went through auto mode, which applies the needed suggestions on its own.
function MessageBody({ m, auto }: { m: Message; auto?: boolean }) {
  const loadEvidenceImage = useContext(EvidenceCtx);
  const { rs, answerSuggestions } = useApp();
  if (m.role !== "agent" || m.kind) {
    if (m.role === "user") {
      return <UserText m={m} />;
    }
    if (isEmptyNote(m)) {
      return <EmptyNote />;
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
        auto={auto}
      />
    );
  }
  return <Markdown content={m.text} />;
}

// isEmptyNote says the message is the placeholder of a conversation with nothing in it yet.
function isEmptyNote(m: Message): boolean {
  return m.kind === "kind" && m.text === NOTHING_YET;
}

// EmptyNote is that placeholder drawn as an empty state: an icon and the sentence on an opaque
// card, centred in the conversation. As a bare note it was small grey capitals straight on the
// background image, and could not be read over its bright patches.
function EmptyNote() {
  return (
    <span class="empty-note-body">
      <svg
        class="empty-note-icon"
        width="28"
        height="28"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.75"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
      </svg>
      <span>{t(NOTHING_YET)}</span>
    </span>
  );
}

function MessageItem({ m, auto }: { m: Message; auto?: boolean }) {
  const answer = m.role === "agent" && !m.kind;
  const empty = choose(isEmptyNote(m), " empty-note", "");
  return (
    <div
      class={`msg ${m.role}${choose(m.kind, " " + m.kind, "")}${empty}`}
      data-who={choose(answer, t("Agent"), undefined)}
    >
      <MessageBody m={m} auto={auto} />
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

// turnMode is the mode auto mode picked for a turn, read from its first step, or null.
function turnMode(shown: StepView[]): "plan" | "task" | null {
  if (shown.length === 0) {
    return null;
  }
  return modeOf(shown[0].text);
}

function Turn({ g, isLast }: { g: Group; isLast: boolean }) {
  const { running, activity, stepsByTurn } = useApp().rs;
  const head = choose(g.items[0].role === "user", g.items[0], null);
  const rest = choose(head, g.items.slice(1), g.items);
  const shown = stepsOf(stepsByTurn, g.turn);
  // The mode auto mode picked, when this turn went through it: its first step says so.
  const mode = turnMode(shown);
  const liveHere = running && isLast;
  // The step in progress is the activity line below, so it is not drawn twice.
  const trail = choose(liveHere && activity, shown.slice(0, -1), shown);
  return (
    <div class="turn">
      {head && (
        <TurnHead head={head} turn={g.turn} liveHere={liveHere} mode={mode} />
      )}
      {trail.length > 0 && <Trail steps={trail} liveHere={liveHere} />}
      {rest.map((m) => (
        <MessageItem key={m.id} m={m} auto={mode !== null} />
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
    // A div, not a main: a `main` cannot take role="log" (its role is fixed), and the log role is
    // what screen readers need here; the explicit role already hid the landmark.
    <div
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
    </div>
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
// The words sit on an opaque card (.no-session-card in index.css): straight on the background
// image they fell on its bright patches and could not be read.
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
      <div class="no-session-card w-full max-w-sm flex flex-col items-center gap-4 px-6 py-6">
        <p class="no-session-title text-base font-semibold">{t("Waiting for a new session")}</p>
        <p class="no-session-hint text-sm">
          {t("Create a session to start talking to the agent.")}
        </p>
        <div class="w-full max-w-[220px] flex">
          <NewSessionButton
            loading={actions.creatingSession === ""}
            onClick={newSession}
            label={t(NEW_SESSION_TITLE)}
          />
        </div>
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
      <div class="flex flex-col flex-1 min-w-0 h-full relative">
        <ChatHeader />
        <NoSession />
      </div>
    );
  }
  return (
    <div class="flex flex-col flex-1 min-w-0 h-full relative">
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

// SettingsHost is the settings dialog, wired to the app: the language of every interface (this
// page, the terminal), saved in the gateway's configuration, and the git host connections. It is
// drawn here and not in the sidebar, whose frosted glass would trap a `fixed` dialog in its width.
function SettingsHost() {
  const {
    closeSettings,
    language,
    projects,
    api: request,
    status,
    voice,
  } = useApp();
  const { changeLanguage } = language;
  const { setGitConnect } = projects;
  const { setToast } = status;
  const onLang = useCallback(
    (s: LangSetting) => void changeLanguage(s),
    [changeLanguage],
  );
  return (
    <SettingsModal
      langSetting={language.uiLangSetting}
      langAvailable={language.uiLangAvailable}
      onLang={onLang}
      onClose={closeSettings}
      voice={{
        state: voice.state,
        hands: voice.hands,
        phrases: voice.info?.wake_phrases || [],
        prefs: voice.prefs,
        setPrefs: voice.setPrefs,
      }}
      git={{
        api: request,
        rev: projects.gitRev,
        onConnect: (service, selfHosted) =>
          setGitConnect({ service, selfHosted }),
        onNotice: (message, error) =>
          setToast({ message, type: choose(error, "error", "success") }),
      }}
    />
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
  useVisualViewport();
  const store = useAppStore(api);
  const pr = usePullRequest(store, api);
  const loadEvidenceImage = useEvidenceLoader(store);
  return (
    <AppCtx.Provider value={store}>
      <EvidenceCtx.Provider value={loadEvidenceImage}>
        <div class="app-shell app-bg flex text-[#e8e8ea] overflow-hidden">
          {/* Blocking auth modal — shown when the browser has no valid credential.
              It covers the entire screen and cannot be dismissed without a token. */}
          {store.auth.authState !== "ok" && <AuthModal />}
          {/* Sidebar — sessions list, toggleable on all sizes. */}
          {store.lists.sidebarOpen && <Sidebar />}
          <MainColumn pr={pr} />
          {/* Before ProjectModals: the git connect dialog it opens stacks on top of it. */}
          {store.showSettings && <SettingsHost />}
          <ProjectModals />
          <DialogModals />
          <LibraryModals />
        </div>
      </EvidenceCtx.Provider>
    </AppCtx.Provider>
  );
}
