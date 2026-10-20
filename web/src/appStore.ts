// The page's state and what can be done with it, gathered in one place and handed to every part
// of the page through a context, so each component reads what it draws.
import { createContext } from "preact";
import { useCallback, useContext, useMemo, useState } from "preact/hooks";
import { useRunState } from "./useRunState";
import { useToastStatus } from "./useToastStatus";
import { useSessionList } from "./useSessionList";
import { useChatScroll } from "./useChatScroll";
import { useModelSwitcher } from "./useModelSwitcher";
import { useSkills } from "./useSkills";
import { useReviewNotes } from "./useReviewNotes";
import { useNotes } from "./useNotes";
import { useProjects } from "./useProjects";
import { useArtifacts } from "./useArtifacts";
import { useSchedules } from "./useSchedules";
import { useConversation } from "./useConversation";
import { useSessionActions } from "./useSessionActions";
import { useDeletion } from "./useDeletion";
import { useDrawers } from "./useDrawers";
import { useStream } from "./useStream";
import { useSuggestions } from "./useSuggestions";
import { useShellApproval } from "./useShellApproval";
import { useComposer } from "./useComposer";
import { useNextSuggestion } from "./useNextSuggestion";
import { useProjectHint } from "./useProjectHint";
import { useCheckpoints } from "./useCheckpoints";
import { useAuth, useLanguage } from "./useAuth";
import { useRemoteChanges } from "./useRemoteChanges";
import { useUpdates } from "./useUpdates";
import { useRowMenus } from "./useRowMenus";
import { useRepoGit } from "./useRepoGit";
import { useFinishedElsewhere } from "./sessionWatch";
import type { Api } from "./prApi";
import { useVoice } from "./useVoice";
import { useSpeech } from "./useSpeech";
import { useVoiceConversation } from "./useVoiceConversation";

export function useAppStore(api: Api) {
  const rs = useRunState();
  const status = useToastStatus();
  const { setState, setToast } = status;
  const lists = useSessionList(api, rs);
  const scroll = useChatScroll({
    messages: rs.messages,
    activity: rs.activity,
    liveThought: rs.liveThought,
    stepsByTurn: rs.stepsByTurn,
  });
  const model = useModelSwitcher(
    api,
    rs.sessionId,
    setState,
    setToast,
    lists.fetchSessions,
  );
  useFinishedElsewhere(lists.sessions, rs.sessionId, setToast);
  const skills = useSkills(api, rs.sessionId, setState);
  const projects = useProjects({ api, lists, setState, setToast });
  const projectHint = useProjectHint({ api, rs, lists, setToast });
  const artifacts = useArtifacts(api, rs.sessionId, setState);
  const notes = useNotes(api, setState);
  const schedules = useSchedules(api, setToast);
  const conv = useConversation({
    api,
    rs,
    lists,
    setState,
    setConfig: model.setConfig,
    requestModal: scroll.requestModal,
  });
  const actions = useSessionActions({
    api,
    rs,
    lists,
    conv,
    setState,
    setToast,
  });
  const deletion = useDeletion({
    api,
    deleteSession: actions.deleteSession,
    deleteSkill: skills.deleteSkill,
    deleteProject: projects.deleteProject,
    sessionsOf: (projectId) =>
      lists.sessions.filter((s) => s.project_id === projectId).map((s) => s.id),
    leaveGone: actions.leaveGone,
  });
  const drawers = useDrawers(rs);
  const shell = useShellApproval({
    api,
    rs,
    lists,
    drawers,
    setState,
    setToast,
  });
  const stream = useStream({ api, rs, lists, conv, setState, setToast });
  const { answerSuggestions, answerQuestions } = useSuggestions(
    api,
    rs,
    stream,
    { reload: conv.transcript, setToast },
  );
  const nextSuggestion = useNextSuggestion({
    api,
    sessionId: rs.sessionId,
    running: rs.running,
    messages: rs.messages,
  });
  const composer = useComposer({
    rs,
    drawers,
    stream,
    runShellLine: shell.runShellLine,
    startSession: actions.createSession,
    nextSuggestion,
  });
  const checkpoints = useCheckpoints({
    api,
    rs,
    conv,
    setInput: composer.setInput,
    setToast,
  });
  const auth = useAuth({
    api,
    rs,
    lists,
    conv,
    setState,
    setSlashCommands: composer.setSlashCommands,
  });
  const updates = useUpdates(api, setToast, auth.authState === "ok");
  useRemoteChanges(lists, setToast, auth.authState === "ok");
  const voice = useVoice({
    api,
    authed: auth.authState === "ok",
    setInput: composer.setInput,
    setToast,
    send: composer.sendQueued,
  });
  const speech = useSpeech({
    messages: rs.messages,
    running: rs.running,
    sessionId: rs.sessionId,
    takeHeard: voice.takeHeard,
    setToast,
    api,
    stepsByTurn: rs.stepsByTurn,
    tts: voice.info?.tts,
    local: voice.info?.local,
    approval: rs.approval,
  });
  // Conversation mode sends what it heard as a new turn, the way the composer would.
  const sendHeard = useCallback(
    (text: string) => {
      if (rs.runningRef.current) {
        return;
      }
      rs.setMessages((prev) => prev.filter((m) => m.kind !== "kind"));
      void stream.submit(text);
    },
    [rs, stream],
  );
  useVoiceConversation({
    on: speech.prefs.conversation,
    ready: voice.state === "ready",
    listenOnce: voice.listenOnce,
    cancelListen: voice.cancelListen,
    send: sendHeard,
    setToast,
  });
  const language = useLanguage(api, auth.authState, setToast);
  const menus = useRowMenus();
  const [showSettings, setShowSettings] = useState(false);
  const openSettings = useCallback(() => setShowSettings(true), []);
  const closeSettings = useCallback(() => setShowSettings(false), []);
  const repoGit = useRepoGit({
    api,
    setState,
    setToast,
    fetchProjects: lists.fetchProjects,
    createSession: actions.createSession,
    submit: stream.submit,
  });
  const reviewNotes = useReviewNotes(
    actions.createSession,
    notes.closeNotes,
    stream.submit,
    closeSettings,
  );
  const selectedSession = useMemo(
    () => lists.sessions.find((s) => s.id === rs.sessionId),
    [lists.sessions, rs.sessionId],
  );
  return {
    api,
    rs,
    status,
    lists,
    scroll,
    model,
    skills,
    projects,
    projectHint,
    artifacts,
    notes,
    reviewNotes,
    schedules,
    conv,
    actions,
    deletion,
    drawers,
    shell,
    stream,
    answerSuggestions,
    answerQuestions,
    composer,
    checkpoints,
    auth,
    updates,
    voice,
    speech,
    language,
    menus,
    repoGit,
    showSettings,
    openSettings,
    closeSettings,
    selectedSession,
    isMerged: selectedSession?.merged === true,
  };
}

export type Store = ReturnType<typeof useAppStore>;

export const AppCtx = createContext<Store | null>(null);

// useApp is the store, for a component under App.
export function useApp(): Store {
  return useContext(AppCtx) as Store;
}
