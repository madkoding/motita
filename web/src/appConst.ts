// The constants the web interface shares: the gateway's statuses and paths, the events the page
// sends itself, the keys it keeps in the browser and how long things take.

// HTTP statuses the gateway answers with that the client tells apart.
export const HTTP_OK = 200;
export const HTTP_NO_CONTENT = 204;
export const HTTP_UNAUTHORIZED = 401;
export const HTTP_FORBIDDEN = 403;
export const HTTP_NOT_FOUND = 404;
export const HTTP_CONFLICT = 409;
export const HTTP_GONE = 410;

// The credentials every request of the page is sent with.
export const SAME_ORIGIN = "same-origin";
export const SESSIONS_URL = "/v1/sessions";
// The endpoint that trades a token for the browser's cookie, and deletes it.
export const WEBUI_SESSION_URL = "/v1/webui/session";

// The gateway's resources, as the paths the requests are built from.
export const SESSIONS_PATH = "/v1/sessions/";
export const SKILLS_PATH = "/v1/skills/";
export const PROJECTS_PATH = "/v1/projects/";
export const SCHEDULES_PATH = "/v1/schedules/";

// The header a JSON request body is sent with.
export const JSON_HEADERS = { "Content-Type": "application/json" };

// The DOM events the page sends itself.
export const AUTH_NEEDED_EVENT = "motita:auth-needed";
export const CHAT_QUIET_EVENT = "motita:chat-quiet";
export const CREATE_SESSION_EVENT = "motita:create-session";

// The title a session has until it is given one.
export const NEW_SESSION_TITLE = "New session";

// Texts used in more than one place.
export const NOT_CONNECTED = "not connected";
export const NEEDS_APPROVAL = "This needs your approval";
export const NOT_KEPT =
  'That computer did not keep "Always allow": it runs an older Motita. Update it there.';
export const TURN_REFUSED = "the gateway refused the turn";
export const NEW_PROJECT = "New project";
export const EDIT_PROJECT = "Edit project";
// The placeholder a conversation with nothing in it yet shows.
export const NOTHING_YET = "Nothing yet. Ask for something below.";

// Lengths of time, in seconds and in milliseconds.
export const HOUR_S = 3600;
export const DAY_S = 86400;
export const DAY_MS = 86_400_000;

// How many characters of a generated id are shown, the way git abbreviates a sha.
export const SHORT_ID_LEN = 7;

// How long the row menu takes to leave: its closing animation in index.css.
export const ROW_MENU_EXIT_MS = 140;
// How long a session that just answered is still counted as answered by this tab.
export const ANSWERED_HOLD_MS = 30000;
// How often the session list is read again while something runs.
export const SESSIONS_POLL_MS = 3000;
// How long the repository field has to stay still before its default branch is asked for.
export const BRANCH_LOOKUP_DELAY_MS = 600;
// After an upgrade: when the gateway is first asked whether it is back, and when to stop asking.
export const RESTART_FIRST_POLL_MS = 2000;
export const RESTART_WAIT_MS = 30000;
export const HEALTH_POLL_MS = 1000;
// A message sent mid-run starts the next run a beat later: the conversation is reloaded twice,
// after the first delay and again after the second.
const HANDOFF_FIRST_RELOAD_MS = 400;
const HANDOFF_SECOND_RELOAD_MS = 1800;
export const HANDOFF_RELOAD_MS = [
  HANDOFF_FIRST_RELOAD_MS,
  HANDOFF_SECOND_RELOAD_MS,
];
// How often the pull request bar is refreshed while the gateway follows the CI.
export const PR_POLL_MS = 20000;
// How long a touch has to be held to open a row's menu.
export const LONG_PRESS_MS = 500;
// How long a dropped stream waits before it is attached again.
export const RECONNECT_MS = 1000;
// How often the update check is repeated: the gateway's hourly check fills the cache this reads.
export const UPDATE_POLL_MS = 10 * 60 * 1000;
// REMOTE_POLL_MS is how often the page re-reads the projects, so the news the gateway found on
// origin (it looks every 15 minutes) reaches an open page.
export const REMOTE_POLL_MS = 300_000;
// PROJECT_FEED_RETRY_MS is how long the page waits before listening again to the gateway's project
// feed after it dropped.
export const PROJECT_FEED_RETRY_MS = 5000;

// PR_LIST_POLL_MS is how often the open pull request overview asks again, and PR_BADGE_POLL_MS how
// often the sidebar asks how many have a failing CI. The gateway remembers its answer for 30 s.
export const PR_LIST_POLL_MS = 30_000;
export const PR_BADGE_POLL_MS = 60_000;
// How long after a project is created its first session is opened.
export const NEW_PROJECT_SESSION_DELAY_MS = 100;
// How long a downloaded artifact's object URL is kept before it is released.
export const DOWNLOAD_URL_TTL_MS = 1000;
// How often the clocks of the scheduled tasks and the running agents tick.
export const CLOCK_TICK_MS = 1000;

// How long a toast stays up: an error longest, then a warning, an update notice and a success.
export const TOAST_ERROR_MS = 12000;
export const TOAST_WARNING_MS = 9000;
export const TOAST_UPDATE_MS = 8000;
export const TOAST_MS = 5000;

// How long the copy buttons say "copied" before they go back to "copy".
export const COPIED_MS = 1500;

// The prefix of a progress line that carries the agent list, for a gateway that forwards it as a
// plain line instead of an `agents` event (agent.AgentsPrefix).
export const AGENTS_PREFIX = "agents: ";

// SUB_PREFIX marks a progress line that is one command a background agent ran or one thought it
// finished (agent.SubPrefix in Go).
export const SUB_PREFIX = "sub: ";

export const STORAGE_KEY = "motita:last-session";
export const SIDEBAR_KEY = "motita:sidebar-open";
export const PROJECT_COLLAPSE_KEY = "motita:collapsed-projects";
export const UPGRADE_DISMISS_KEY = "motita:upgrade-dismissed";

// How long the loading modal takes to leave, in milliseconds. It must match the
// `chat-modal-leaving` transition in index.css: the state is held for exactly this long so the
// fade-out can finish before the node is removed. Measured: without it the modal vanished in ONE
// frame, because a node that is unmounted cannot transition.
export const MODAL_EXIT_MS = 220;

// STEPS_MAX mirrors the gateway's cap on the steps one turn keeps.
export const STEPS_MAX = 300;

// SHELL_LOG_MAX bounds the terminal history so a very long session cannot grow it forever.
export const SHELL_LOG_MAX = 500;

// NO_RESULT closes a command that never reported an exit code.
export const NO_RESULT = -999;

// REASONING_LEVELS are the levels the gateway accepts, in the order they are offered.
export const REASONING_LEVELS = ["off", "low", "medium", "high"];

// The fields of a server-sent event this client reads.
export const SSE_ID = "id: ";
export const SSE_EVENT = "event: ";
export const SSE_DATA = "data: ";
// What separates two server-sent event frames.
export const SSE_FRAME_END = "\n\n";

// MENU_W / MENU_H are the row menu's own box, and they are constants rather than a measurement
// taken after render because the position has to be decided BEFORE the menu is drawn: a menu
// that renders first and is moved afterwards is a menu the reader sees jump.
//
// They mirror the markup — `min-w-[200px]` (so MENU_W is exact and the menu's right edge lines up
// with the button's), one `py-1` and up to four items of ~36px — and the flip decision only needs to be approximately right: being a few pixels out means the menu sits a
// few pixels away from the ideal, not that it is clipped.
export const MENU_W = 200;
export const MENU_H = 160;
// The gap between a row menu and its trigger, and between the menu and the window's edge.
export const MENU_GAP = 4;

// The long-press menu's room: how far from the right and the bottom edge it must stay.
export const CONTEXT_MENU_W = 180;
export const CONTEXT_MENU_H = 120;

// How close to the end of the conversation counts as "following along": one flick of the wheel is
// ~100px, so anything under this is somebody who has not deliberately scrolled away.
export const BOTTOM_SLACK = 120;

// The queue the terminal drawer plays: one entry at a time, in order, only while it is open. When
// commands arrive faster than they can be typed the terminal catches up by typing FASTER, and
// never by drawing an entry whole - that read as "everything at once". The only cut is a sanity
// cap: a history this far behind is not being watched.
export const TERM_CAP = 30;
// What is left to type once the cap is hit: the last few entries.
export const TERM_KEEP = 6;
// The fastest the terminal types when it is behind.
export const TERM_BOOST_MAX = 9;

// The most matching commands the slash popup lists.
export const SLASH_POPUP_MAX = 8;

// The options of the approval panel, in the order the keyboard walks them.
export const APPROVAL_RUN = 0;
export const APPROVAL_ALL = 1;
export const APPROVAL_NO = 2;
export const APPROVAL_OPTIONS = 3;
// How long a new question is on screen before Enter answers it: an Enter typed in that moment was
// meant for what had focus before (the composer), not for a question nobody has read yet.
export const APPROVAL_ARM_MS = 500;

// The most attempts a project can allow to fix a failing CI.
export const PR_MAX_FIXES_LIMIT = 20;

// The artifact sizes are shown in kilobytes.
export const KB = 1024;

// An artifact expiring within this many days says so.
export const EXPIRY_WARN_DAYS = 7;

// The id of the composer's text field.
export const TASK_INPUT_ID = "task";
