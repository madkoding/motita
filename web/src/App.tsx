import { createContext } from "preact";
import { memo } from "preact/compat";
import {
  useState,
  useEffect,
  useCallback,
  useContext,
  useMemo,
} from "preact/hooks";
import { Markdown } from "./Markdown";
import { Lightbox } from "./Lightbox";
import { ReportCard } from "./Report";
import { QuestionsCard } from "./QuestionsCard";
import type { QuestionAnswers } from "./questions";
import type { SuggestionAnswers } from "./proposals";
import "./report.css";
import { copyText } from "./clipboard";
import { t, useLang, type LangSetting } from "./i18n";
import { SettingsModal } from "./Settings";
import { FolderBrowser } from "./FolderBrowser";
import { GitConnectModal } from "./GitConnect";
import { PRBar, usePullRequest, type PullRequest } from "./PullRequestBar";
import { choose } from "./choose";
import { modeOf } from "./appParse";
import { clearCredential } from "./appAuth";
import { AppCtx, useApp, useAppStore, type Store } from "./appStore";
import { sameTurn, TurnCtx, useTurn, useTurnView } from "./turnView";
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
import { ConnectionBanner } from "./ConnectionBanner";
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
  SubagentsStrip,
  TurnTop,
} from "./ConversationParts";
import { EditProjectModal } from "./EditProjectModal";
import { RepoGitPanel } from "./RepoGitPanel";
import { GitActivityModal } from "./GitActivityModal";
import { NewProjectModal } from "./NewProjectModal";
import { ModelSwitcherModal } from "./ModelSwitcherModal";
import { ConfirmDeleteModal, ContextMenu } from "./ConfirmDeleteModal";
import { Subtitles } from "./Subtitles";
import { ProjectHintCard } from "./ProjectHintCard";
import { ToastCard } from "./ToastCard";
import { UpgradeModal } from "./UpgradeModal";
import { SkillLibraryModal } from "./SkillLibraryModal";
import { NotesModal } from "./NotesModal";
import { ArtifactsModal } from "./ArtifactsModal";
import { ScheduledTasksModal } from "./ScheduledTasksModal";
import { useVisualViewport } from "./viewport";
import { machineApi } from "./machinesApi";
import { MachinesCtx, useMachineChoice, useMachines } from "./useMachines";
import { ConnectMachineModal } from "./ConnectMachineModal";

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
  // One cache per session: its object URLs are let go when another session is shown, or the page
  // leaves, so a tab kept open does not hold every screenshot it ever showed.
  const evidenceUrls = useMemo(
    () => new Map<string, Promise<string | null>>(),
    [sessionId],
  );
  useEffect(
    () => () => {
      for (const p of evidenceUrls.values()) {
        void p.then((url) => url && URL.revokeObjectURL(url));
      }
    },
    [evidenceUrls],
  );
  const loadEvidenceImage = useCallback(
    (name: string) => {
      let p = evidenceUrls.get(name);
      if (!p) {
        p = api(artifactBase() + "/" + encodeURIComponent(name))
          .then(async (res) => {
            if (!res.ok) {
              return null;
            }
            return URL.createObjectURL(await res.blob());
          })
          .catch(() => null);
        evidenceUrls.set(name, p);
      }
      return p;
    },
    [evidenceUrls, artifactBase],
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
  const { running, lastUserId, answerSuggestions, answerQuestions } = useTurn();
  if (m.role !== "agent" || m.kind) {
    // A user's message is never here: each one opens its turn and is drawn by TurnHead.
    if (isEmptyNote(m)) {
      return <EmptyNote />;
    }
    return <span>{choose(m.kind === "kind", t(m.text), m.text)}</span>;
  }
  if (m.questions) {
    // The agent asked before doing anything: the questions are answered on a card, while nothing
    // runs, and they are stale once the conversation moved on without them.
    const onAnswer = choose(
      running,
      undefined,
      (a: QuestionAnswers, shown: string) => answerQuestions(m, a, shown),
    );
    return (
      <QuestionsCard
        items={m.questions}
        origin={m.origin ?? ""}
        answers={m.questionAnswers}
        expired={!m.questionAnswers && lastUserId > m.id}
        onAnswer={onAnswer}
      />
    );
  }
  if (m.report) {
    // The suggestions of a report are answered only while nothing runs: applying them is a turn.
    const onAnswer = choose(running, undefined, (a: SuggestionAnswers) =>
      answerSuggestions(m, a),
    );
    // Suggestions left unanswered once the conversation moved on are stale: the work they were
    // about has changed since, so they are no longer offered.
    const expired = !m.answers && lastUserId > m.id;
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

// SpeakButton reads an answer aloud, or stops it while it is the one being read.
function SpeakButton({ m }: { m: Message }) {
  const { speakingId, speechSupported, running, lastMsgId, speak } = useTurn();
  const reading = speakingId === m.id;
  // An answer still streaming has no end yet: it is read once it is complete.
  if (!speechSupported || (running && m.id === lastMsgId)) {
    return null;
  }
  const label = choose(reading, t("Stop reading"), t("Read aloud"));
  return (
    <button
      type="button"
      class={"speak-msg-btn" + choose(reading, " is-reading", "")}
      aria-label={label}
      aria-pressed={reading}
      aria-keyshortcuts="Alt+L"
      title={label + " (Alt+L)"}
      onClick={() => speak(m)}
    >
      <SpeakIcon reading={reading} />
    </button>
  );
}

// SpeakIcon is the stop square while the answer is being read, the speaker otherwise.
function SpeakIcon({ reading }: { reading: boolean }) {
  if (reading) {
    return <IconStop />;
  }
  return <IconSpeaker />;
}

function IconSpeaker() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M11 5 6 9H2v6h4l5 4V5z" />
      <path d="M15.5 8.5a5 5 0 0 1 0 7" />
      <path d="M19 5a10 10 0 0 1 0 14" />
    </svg>
  );
}

function IconStop() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true">
      <rect x="6" y="6" width="12" height="12" rx="1.5" fill="currentColor" />
    </svg>
  );
}

type FlagState = "idle" | "open" | "sending" | "sent";

// FlagButton is "this was not right" on an answer: it opens a small form for what went wrong (the
// words are optional) and writes the answer, with what the gateway knows of its turn, in the
// friction journal. The journal is local and changes nothing by itself.
function FlagButton({ m }: { m: Message }) {
  const { running, lastMsgId, flagAnswer } = useTurn();
  const [state, setState] = useState<FlagState>("idle");
  const [note, setNote] = useState("");
  const [failed, setFailed] = useState(false);
  // An answer still streaming has no end yet, and one without a turn has nothing to point at.
  if (m.turn == null || (running && m.id === lastMsgId)) {
    return null;
  }
  const send = async () => {
    setState("sending");
    setFailed(false);
    const ok = await flagAnswer(m, note.trim());
    setFailed(!ok);
    setState(choose(ok, "sent", "open"));
  };
  if (state === "sent") {
    return <output class="flag-msg-done">{t("Noted, thanks")}</output>;
  }
  return (
    <>
      <button
        type="button"
        class={"flag-msg-btn" + choose(state === "idle", "", " is-open")}
        aria-label={t("This was not right")}
        aria-expanded={state !== "idle"}
        title={t("This was not right")}
        onClick={() => setState(choose(state === "idle", "open", "idle"))}
      >
        <IconThumbDown />
      </button>
      {state !== "idle" && (
        <form
          class="flag-form"
          onSubmit={(e) => {
            e.preventDefault();
            void send();
          }}
        >
          <textarea
            class="flag-form-note"
            rows={2}
            maxLength={500}
            placeholder={t("What went wrong? (optional)")}
            aria-label={t("What went wrong? (optional)")}
            value={note}
            onInput={(e) => setNote((e.target as HTMLTextAreaElement).value)}
          />
          <p class="flag-form-hint">
            {choose(
              failed,
              t("could not note it"),
              t(
                "It stays in the journal on this computer; nothing is sent anywhere.",
              ),
            )}
          </p>
          <button
            type="submit"
            class="flag-form-send"
            disabled={state === "sending"}
          >
            {t("Send")}
          </button>
        </form>
      )}
    </>
  );
}

function IconThumbDown() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M10 15v4a3 3 0 0 0 3 3l4-9V2H5.7a2 2 0 0 0-2 1.7l-1.4 9a2 2 0 0 0 2 2.3z" />
      <path d="M17 2h3a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-3" />
    </svg>
  );
}

function MessageItem({ m, auto }: { m: Message; auto?: boolean }) {
  const answer = m.role === "agent" && !m.kind && !m.draft;
  const empty = choose(isEmptyNote(m), " empty-note", "");
  return (
    <div
      class={`msg ${m.role}${choose(m.kind, " " + m.kind, "")}${empty}`}
      data-who={choose(answer, t("Agent"), undefined)}
      data-msg-id={m.id}
    >
      <MessageBody m={m} auto={auto} />
      {m.changes && <ChangesCard changes={m.changes} />}
      {m.artifacts && <RunArtifacts artifacts={m.artifacts} />}
      {answer && <FlagButton m={m} />}
      {answer && <SpeakButton m={m} />}
    </div>
  );
}

// stepsOf are the steps a turn shows: thoughts live in the thinking drawer, the chat keeps steps
// and actions.
function stepsOf(steps: StepView[] | undefined): StepView[] {
  return (steps || []).filter((st) => st.kind !== "thought");
}

// turnMode is the mode auto mode picked for a turn, read from its first step, or null.
function turnMode(shown: StepView[]): "plan" | "task" | null {
  if (shown.length === 0) {
    return null;
  }
  return modeOf(shown[0].text);
}

interface TurnProps {
  g: Group;
  /** The turn's steps as the run state keeps them: the same array until that turn changes. */
  steps: StepView[] | undefined;
  /** Whether this is the turn the run in flight is writing. */
  liveHere: boolean;
  /** What the agent is doing right now; null for every turn but the live one. */
  activity: string | null;
}

function TurnBody({ g, steps, liveHere, activity }: TurnProps) {
  const head = choose(g.items[0].role === "user", g.items[0], null);
  const rest = choose(head, g.items.slice(1), g.items);
  const shown = stepsOf(steps);
  // The mode auto mode picked, when this turn went through it: its first step says so.
  const mode = turnMode(shown);
  // The step in progress is the activity line below, so it is not drawn twice.
  const trail = choose(liveHere && activity, shown.slice(0, -1), shown);
  return (
    <div class="turn">
      <TurnTop
        head={head}
        turn={g.turn}
        liveHere={liveHere}
        mode={mode}
        steps={trail}
      />
      {rest.map((m) => (
        <MessageItem key={m.id} m={m} auto={mode !== null} />
      ))}
      {liveHere && activity && (
        <ActivityLine activity={activity} shown={shown} />
      )}
    </div>
  );
}

// Turn is one input with its steps and answer. Memoized: while the last turn streams, the others
// are skipped instead of diffed again on every token.
const Turn = memo(TurnBody, sameTurn);

// Conversation — the decorative gradient lives here only.
function Conversation() {
  const store = useApp();
  const { rs, scroll } = store;
  const turnView = useTurnView(store);
  const groups = groupsOf(rs.messages);
  return (
    // The wrapper holds the conversation and the fade fixed over its top edge, which sits behind
    // the pinned input (see .chat-fade).
    <div class="chat-wrap relative flex-1 min-h-0 flex flex-col">
      <div class="chat-fade" aria-hidden="true" />
      {/* A div, not a main: a `main` cannot take role="log" (its role is fixed), and the log role is
        what screen readers need here; the explicit role already hid the landmark. */}
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
        <TurnCtx.Provider value={turnView}>
          {groups.map((g, gi) => {
            const liveHere = rs.running && gi === groups.length - 1;
            return (
              <Turn
                key={g.key}
                g={g}
                steps={choose(
                  g.turn != null,
                  rs.stepsByTurn[g.turn!],
                  undefined,
                )}
                liveHere={liveHere}
                activity={choose(liveHere, rs.activity, null)}
              />
            );
          })}
        </TurnCtx.Provider>
        <SubagentsStrip />
      </div>
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
// the click was not on one. Only a button counts: an answer may carry the class and the data-*
// payload of its own (raw HTML), but never a button, so a look-alike cannot pick what is copied.
function copyTextOf(target: HTMLElement): string | null {
  if (target.tagName !== "BUTTON") {
    return null;
  }
  if (target.classList.contains("copy-btn")) {
    return target.dataset.copyText || "";
  }
  if (target.classList.contains("copy-msg-btn")) {
    // The message keeps its source on its .markdown-body, the button's parent (Markdown.tsx).
    return target.closest<HTMLElement>(".markdown-body")?.dataset.raw || "";
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
// deleted. Nothing is created behind the user's back; the chat waits, and says how to start one:
// the composer is there, and the first message typed opens the conversation it goes to.
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
        <p class="no-session-title text-base font-semibold">
          {t("Waiting for a new session")}
        </p>
        <p class="no-session-hint text-sm">
          {t(
            "Type below to start talking to the agent, or create a session first.",
          )}
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

// MainColumn is the header, the conversation, the drawers and panels over it, the subtitles of
// the voice, and the composer.
// `relative` is what anchors the chat spinner to THIS column: without it the spinner would be
// positioned against the nearest positioned ancestor, or the window, and would cover the sidebar.
function MainColumn({ pr }: { pr: PullRequest }) {
  const { rs, scroll, speech } = useApp();
  if (rs.noSession) {
    return (
      <div class="flex flex-col flex-1 min-w-0 h-full relative">
        <ChatHeader />
        <NoSession />
        <Composer />
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
      <Subtitles on={speech.prefs.subtitles} size={speech.prefs.subtitleSize} />
      {rs.approval && <ApprovalPanel approval={rs.approval} />}
      <PRBar pr={pr} />
      <ProjectHintCard />
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
    speech,
    rs,
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
        info: voice.info,
        api: request,
        onInfo: voice.setInfo,
      }}
      speech={{
        supported: speech.supported,
        prefs: speech.prefs,
        setPrefs: speech.setPrefs,
        voices: speech.voices,
        test: speech.test,
        device: speech.device,
        gatewayVoices: speech.gatewayVoices,
        allVoices: speech.allVoices,
        canListen: voice.state === "ready",
        local: speech.local,
        api: request,
      }}
      llm={{
        api: request,
        onNotice: (message, error) =>
          setToast({ message, type: choose(error, "error", "success") }),
      }}
      security={{ api: request, sessionId: rs.sessionId }}
      codeIndex={{ api: request }}
      autopilot={{ api: request }}
      network={{ api: request }}
      push={{ api: request }}
      friction={{ api: request }}
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

// pickerStart is the folder the folder browser opens on: the project's own for the edit dialog, and
// the one already chosen for the new project dialog.
function pickerStart(
  picker: "new" | "edit",
  projects: ReturnType<typeof useApp>["projects"],
): string | undefined {
  return choose(
    picker === "edit",
    projects.editProject?.path || projects.editProject?.dir,
    projects.newProjectPath,
  );
}

// ProjectModals are the dialogs of a project: editing it and creating it, with the folder browser
// either one can open, and the git host connection a pull request can need.
function ProjectModals() {
  const { api: request, projects, repoGit } = useApp();
  const { editProject, showNewProject, folderPicker, gitConnect } = projects;
  return (
    <>
      {editProject && <EditProjectModal p={editProject} />}
      {repoGit.open && <RepoGitPanel r={repoGit.open} />}
      <GitActivityModal />
      {showNewProject && <NewProjectModal />}
      {folderPicker && (
        <FolderBrowser
          api={request}
          start={pickerStart(folderPicker, projects)}
          onPick={projects.pickFolder}
          onClose={projects.closeFolderPicker}
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
      {status.toast && (
        // Keyed by its text: a different toast is a new card, which enters again.
        <ToastCard
          key={status.toast.type + ":" + status.toast.message}
          toast={status.toast}
        />
      )}
      {updates.showUpgrade && <UpgradeModal />}
    </>
  );
}

// LibraryModals are the skill library, the image preview, the artifacts and the scheduled tasks.
function LibraryModals() {
  const { skills, artifacts, schedules, notes } = useApp();
  return (
    <>
      {skills.showSkillLibrary && <SkillLibraryModal />}
      <ImagePreview />
      {artifacts.showArtifacts && <ArtifactsModal />}
      {notes.showNotes && <NotesModal />}
      {schedules.showScheduledTasks && <ScheduledTasksModal />}
    </>
  );
}

// MachineApp is the whole page for one computer: request reaches it (this one, or a connected one
// through this gateway). It is keyed by the computer, so showing another one starts its page fresh
// rather than mixing two computers' sessions in one state.
function MachineApp({
  request,
  machine,
  setMachine,
}: {
  request: typeof api;
  machine: string;
  setMachine: (id: string) => void;
}) {
  const store = useAppStore(request);
  const pr = usePullRequest(store, request);
  const loadEvidenceImage = useEvidenceLoader(store);
  // The computers are always this gateway's: api, never the computer's own request.
  const machines = useMachines(
    api,
    store.auth.authState === "ok",
    machine,
    setMachine,
  );
  return (
    <AppCtx.Provider value={store}>
      <MachinesCtx.Provider value={machines}>
        <EvidenceCtx.Provider value={loadEvidenceImage}>
          <div class="app-shell app-bg flex text-[#e8e8ea] overflow-hidden">
            {/* Blocking auth modal — shown when the browser has no valid credential.
              It covers the entire screen and cannot be dismissed without a token. */}
            {store.auth.authState !== "ok" && <AuthModal />}
            {store.auth.authState === "ok" && <ConnectionBanner />}
            {/* Sidebar — sessions list, toggleable on all sizes. */}
            {store.lists.sidebarOpen && <Sidebar />}
            <MainColumn pr={pr} />
            {/* Before ProjectModals: the git connect dialog it opens stacks on top of it. */}
            {store.showSettings && <SettingsHost />}
            <ProjectModals />
            <DialogModals />
            <LibraryModals />
            {machines.connecting && <ConnectMachineModal />}
          </div>
        </EvidenceCtx.Provider>
      </MachinesCtx.Provider>
    </AppCtx.Provider>
  );
}

export default function App() {
  // Every component under App re-renders when the language changes: App itself subscribes.
  useLang();
  useVisualViewport();
  const [machine, setMachine] = useMachineChoice();
  const request = useMemo(() => machineApi(api, machine), [machine]);
  return (
    <MachineApp
      key={machine}
      request={request}
      machine={machine}
      setMachine={setMachine}
    />
  );
}
