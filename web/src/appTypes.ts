// The shapes the web interface reads from the gateway and keeps in its own state.
import type { TaskReport } from "./Report";
import type { PRWatch, ToastType } from "./prApi";
import type { SuggestionAnswers } from "./proposals";
import type { AskItem, QuestionAnswers } from "./questions";
import type { SelfHosted } from "./GitConnect";

export interface Message {
  id: number;
  role: "user" | "agent" | "activity";
  text: string;
  kind?: string;
  // changes is what the run changed: pictures first, files as a summary.
  changes?: ChangeReport;
  // report is the answer as structure, when the run produced one: drawn as a card.
  report?: TaskReport;
  // artifacts are the files this run saved for the person, offered under the answer.
  artifacts?: { name: string; type: string; size: number }[];
  // turn, on a message of the user's, is the checkpoint that input became: the number the
  // gateway knows it by. It is unset until the gateway has said which one it is. On an answer it
  // is the turn that answer ended.
  turn?: number;
  // shown, on a message of the user's, is what is drawn instead of the text sent: the turn that
  // applies accepted suggestions reads "Applying suggestions", not the prompt it carries.
  shown?: string;
  // answers, on an answer with suggestions, is how they were answered.
  answers?: SuggestionAnswers;
  // questions, on an answer, are what the agent asked before doing anything: drawn as a card to
  // answer. origin is the request they are about, sent again with the answers.
  questions?: AskItem[];
  origin?: string;
  // questionAnswers, on an answer with questions, is how they were answered.
  questionAnswers?: QuestionAnswers;
  // draft marks the answer while it is being written: it grows with each snapshot and becomes the
  // answer itself, the same message, when the run ends. A draft is not read aloud.
  draft?: boolean;
}

export interface ChangeReport {
  files?: { path: string; status: string; added: number; deleted: number }[];
  previews?: { name: string; url: string }[];
}

export interface PendingApproval {
  id: string;
  question?: string;
  reason?: string;
  command?: string;
}

export interface SessionInfo {
  id: string;
  title: string;
  project_id?: string;
  branch?: string;
  mergeable?: boolean;
  merged?: boolean;
  merged_sha?: string;
  continuable?: boolean;
  changes?: number;
  worktree?: string;
  // pr_watch is what the gateway is doing about the session's pull request: following its CI,
  // fixing it, or done. It is what the pull request toasts are drawn from.
  pr_watch?: PRWatch;
  // auto_approve is set while the user has allowed every command in this session.
  auto_approve?: boolean;
  created: string;
  last_used: string;
  running: boolean;
  // agents_running counts the background agents the session's run started that are still
  // working. Older gateways do not send it.
  agents_running?: number;
  // provider and model are what this session's next turn runs with: each session keeps its own.
  // Older gateways do not send them.
  provider?: string;
  model?: string;
}

// AgentInfo is one agent of a run, as the gateway reports it (agent.AgentInfo). The row with no
// parent is the main agent; the others are background agents it started.
export interface AgentTokens {
  input: number;
  output: number;
  cache_read?: number;
  cache_write?: number;
}

export interface AgentInfo {
  id: string;
  parent?: string;
  purpose: string;
  state: string; // running | passed | failed | cancelled
  started: string;
  finished?: string;
  elapsed_ms: number;
  tokens: AgentTokens;
  calls: number;
  round?: number;
  activity?: string;
  branch?: string;
  summary?: string;
}

export interface ProjectInfo {
  id: string;
  title: string;
  description?: string;
  dir: string;
  git_url?: string;
  podman?: boolean;
  branch?: string;
  // main_branch is the branch the project goes back to when it has no sessions.
  main_branch?: string;
  // merge_method is how its pull requests are merged from here: merge, squash or rebase.
  merge_method?: string;
  // pr_max_fixes is how many times the agent is sent to fix a failing CI (0: the default).
  pr_max_fixes?: number;
  // auto_merge merges a pull request by itself once its CI passes.
  auto_merge?: boolean;
  // auto_continue opens a new session from the updated branch when a pull request is merged.
  auto_continue?: boolean;
  changes?: number;
  created: string;
  // repos are the project's other repositories, after the main one in dir.
  repos?: ProjectRepoInfo[];
}

// ProjectRepoInfo is one of a project's other repositories, cloned inside its folder.
export interface ProjectRepoInfo {
  name: string;
  git_url: string;
  dir: string;
  main_branch?: string;
  branch?: string;
  changes?: number;
}

export interface ConfigView {
  provider: string;
  model: string;
  reasoning: string;
  reasoning_enabled: boolean;
  api_key_present: boolean;
  // Set only on the answer to a model change: "history" when the conversation so far was written
  // with another model, "running" when the turn in flight finishes with the old one.
  warnings?: string[];
  previous_model?: string;
}

export interface ProviderInfo {
  id: string;
  name: string;
  models: string[];
  fetch_models: boolean;
  key_present: boolean;
  is_current: boolean;
}

export interface ScheduledTask {
  id: string;
  title: string;
  task: string;
  kind: string;
  session_id: string;
  every: string;
  enabled: boolean;
  created: string;
  last_run?: string;
  last_outcome?: string;
  run_count: number;
  next_run: string;
}

// SkillInfo is one skill as /v1/skills publishes it: the index fields plus the lifecycle
// telemetry the curator keeps about it. There is no body here on purpose — the index stays
// cheap to fetch, and the body arrives when the user opens one document.
export interface SkillInfo {
  name: string;
  title: string;
  summary: string;
  path: string;
  created_by?: "agent" | "foreground";
  state?: "active" | "stale" | "archived";
  pinned?: boolean;
  disabled?: boolean;
  use_count: number;
  view_count: number;
  patch_count: number;
  last_used_at?: string;
}

// UpdateInfo is what /v1/update/check returns.
export interface UpdateInfo {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  release_url?: string;
  release_name?: string;
  published_at?: string;
  error?: string;
}

// UpgradeProgressEvent is one SSE event from /v1/update/run.
export interface UpgradeProgressEvent {
  stage: string;
  percent?: number;
  message?: string;
  version?: string;
}

// StepView is one thing the agent did during a turn, as the gateway keeps it. Steps are the
// history of the work: they stay under the input that caused them, and survive a restart.
// A thought is a step too, but it is shown in the thinking drawer and not in the chat.
export interface StepView {
  kind: string;
  text: string;
  // exit is a command's result once it has finished: null while it runs.
  exit?: number | null;
  out?: string;
  // key tells the steps of a trail apart when they are drawn. It is given once, when the step
  // is received, so it never changes while the trail is on screen.
  key?: number;
}

// CheckpointInfo is what the client needs to know about a checkpoint besides its steps.
export interface CheckpointInfo {
  // files: the checkpoint kept the state of the files, so they can be restored too.
  files: boolean;
}

// ShellEntry is one command the agent ran, with everything it printed. The conversation
// only shows that the command ran and whether it worked; the full input and output live
// in the terminal drawer, as a history.
export interface ShellEntry {
  id: number;
  cmd: string;
  exit: number | null;
  out: string;
}

// Toast is the notification at the bottom of the screen.
// `detail` is the second line a user reads — the human explanation.
// `technical` is the raw error text, shown small and collapsed behind a
// "Details" toggle: it names the file that conflicted, which is the one thing
// a user needs in order to act, without putting git's own words in their face.
// `update` marks the "new version" toast: it is clickable and stays longer. It is a flag and not
// a test on the text, because the text is translated.
export interface Toast {
  message: string;
  type: ToastType;
  detail?: string;
  technical?: string;
  update?: boolean;
}

// SetToast shows a toast, or takes it down with null.
export type SetToast = (
  t: Toast | null | ((prev: Toast | null) => Toast | null),
) => void;

// SetStatus drives the header pill; `bad` also announces the failure in a toast.
export type SetStatus = (text: string, bad?: boolean) => void;

// RowTarget is the session or project a row menu or a long press is about.
export interface RowTarget {
  type: "session" | "project";
  id: string;
  title: string;
}

// ContextMenuAt is the long-press menu: what it is about and where the finger was.
export interface ContextMenuAt extends RowTarget {
  x: number;
  y: number;
}

// ConfirmTarget is what the delete confirmation is about.
export interface ConfirmTarget {
  type: "session" | "project" | "skill";
  id: string;
  title: string;
}

// FileChange is one uncommitted change a deletion would discard.
export interface FileChange {
  path: string;
  kind: string;
  from?: string;
}

// DeletionPreview is what the gateway says a deletion would discard.
export interface DeletionPreview {
  worktree?: string;
  branch?: string;
  changes: FileChange[];
  sessions?: { id: string; title: string; changes: FileChange[] }[];
  count: number;
  inspection_failed?: boolean;
  error?: string;
}

// EditProjectDraft is what the edit-project dialog holds while it is open.
export interface EditProjectDraft {
  id: string;
  title: string;
  description: string;
  main: string;
  branches: string[];
  current: string;
  mergeMethod: string;
  prMaxFixes: number;
  autoMerge: boolean;
  autoContinue: boolean;
  // The repositories, which change at once rather than on Save: adding one clones it.
  mainRepo: { name: string; source: string; movable?: boolean } | null;
  repos: ProjectRepoInfo[];
}

// GitConnectRequest opens the git host connect modal; `after` says what to do once the host is
// connected: pick a repository, or send the create again.
export interface GitConnectRequest {
  service?: string;
  selfHosted?: SelfHosted;
  after?: "picker" | "create";
}

// ArtifactItem is one file of the artifact list.
export interface ArtifactItem {
  name: string;
  size: number;
  type: string;
  modified: string;
  pinned?: boolean;
  expires_at?: string;
}

// ArtifactView is the file being looked at: `body` is the text of a text file, `url` a blob for an
// image or a PDF.
export interface ArtifactView {
  name: string;
  type: string;
  body?: string;
  url?: string;
}

export type ArtifactScope = "session" | "project";

// SlashCommand is one command the composer offers after a slash.
export interface SlashCommand {
  name: string;
  aliases: string[];
  help: string;
  arg: string;
  group: string;
}

// ActiveTag is a slash command the composer shows as a pill above the text. `key` is given when
// the tag is added, so removing one tag never renames the others.
export interface ActiveTag {
  name: string;
  group: string;
  key: number;
}

export type UrlMode = "push" | "replace" | "none";
