// Pull request and CI of a session: the client of the gateway's /pr endpoints, and the pure rules
// that decide when the user is told something and when the agent is sent to fix the CI. No value
// imports: the node tests load this file as is.

export type Api = (path: string, init?: RequestInit) => Promise<Response>;

export type CIState = "pending" | "success" | "failure" | "none";

export interface CICheck {
  name: string;
  state: CIState;
  url?: string;
  detail?: string;
}

// What the host says about a merge: possible now, refused for a reason, or not known.
export type MergeCode =
  "ok" | "blocked" | "conflict" | "draft" | "behind" | "unknown";

export interface PRView {
  // 'none' while the branch has no open pull request, 'open' once it has.
  state: "none" | "open";
  branch: string;
  pr?: { number: number; url: string; title?: string };
  ci?: { state: CIState; checks: CICheck[]; rev?: string };
  // Whether the host would accept a merge now; asked once the CI has passed.
  merge?: { code: MergeCode };
  // What the gateway is doing about the CI; absent when nothing.
  watch?: PRWatch;
  // The repositories with work to propose, when the session's folder has work in two or more.
  repos?: PRRepo[];
}

// mergeRefused: the host would turn a merge down for a reason the user can see and fix.
export function mergeRefused(v: PRView | null | undefined): boolean {
  const c = v?.merge?.code;
  return c === "blocked" || c === "conflict" || c === "draft";
}

// PRRef is the pull request a view points at.
export type PRRef = NonNullable<PRView["pr"]>;

// PRError carries the HTTP status and the machine code of a refusal (git_auth_required,
// pr_no_remote, pr_no_host, pr_exists).
export class PRError extends Error {
  status: number;
  code?: string;
  service?: string;
  pr?: PRRef;
  constructor(
    message: string,
    status: number,
    code?: string,
    service?: string,
    pr?: PRRef,
  ) {
    super(message);
    this.status = status;
    this.code = code;
    this.service = service;
    this.pr = pr;
  }
}

async function failure(res: Response): Promise<PRError> {
  const body = await res.json().catch(() => ({}));
  return new PRError(
    body.error || "HTTP " + res.status,
    res.status,
    body.code,
    body.service,
    body.pr,
  );
}

const base = (sid: string) => "/v1/sessions/" + encodeURIComponent(sid) + "/pr";

export async function getPR(api: Api, sid: string): Promise<PRView> {
  const res = await api(base(sid));
  if (!res.ok) {
    throw await failure(res);
  }
  return res.json();
}

// One repository of a session's folder with work to propose, and where its pull request stands.
export interface PRRepo {
  // What a request names the repository by; "." is the session's folder itself.
  name: string;
  state: "none" | "open";
  pr?: PRRef;
  ci?: CIState;
}

// retryFix sends the agent to fix the CI now and has the gateway follow it again from the start:
// what "Try again" does once the gateway gave up. started is false when the agent was busy and the
// message was queued behind its turn.
export async function retryFix(
  api: Api,
  sid: string,
): Promise<{ started: boolean }> {
  const res = await api(base(sid) + "/fix", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (!res.ok) {
    throw await failure(res);
  }
  const body = await res.json().catch(() => ({}));
  return { started: body.started === true };
}

// mergePR merges the pull request. The gateway refuses unless its CI has passed.
export async function mergePR(api: Api, sid: string): Promise<void> {
  const res = await api(base(sid) + "/merge", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

// ── What the gateway is doing about the CI ──────────────────────────────────

// The gateway follows the CI of a pull request by itself and reports it in the session list; this
// side only decides what is worth a toast.
export type WatchStatus =
  | "following"
  | "fixing"
  | "passed"
  | "gave_up"
  | "no_ci"
  | "merged"
  | "base_red"
  | "closed"
  | "reviewing"
  | "resolving";

export interface PRWatch {
  status: WatchStatus;
  attempts: number;
  max: number;
  number?: number;
  // The session opened by itself when the pull request was merged (the project's auto_continue).
  next?: string;
}

export type Notice =
  | "fixing"
  | "passed"
  | "gave_up"
  | "no_ci"
  | "merged"
  | "base_red"
  | "closed"
  | "reviewing"
  | "resolving"
  | null;

// prNotice says what the user is to be told when the watch of a session moves from prev to cur.
// prev is undefined the first time the session is seen: what happened before the tab looked is not
// news, and a toast for it would be noise.
export function prNotice(
  prev: PRWatch | null | undefined,
  cur: PRWatch | null | undefined,
): Notice {
  if (prev === undefined || !cur) {
    return null;
  }
  // Sending the agent is news again at every new attempt, not only when the state changes.
  if (
    cur.status === "fixing" ||
    cur.status === "reviewing" ||
    cur.status === "resolving"
  ) {
    if (prev?.status !== cur.status || cur.attempts > prev.attempts) {
      return cur.status;
    }
    return null;
  }
  if (cur.status === "following") {
    return null;
  }
  if (prev?.status === cur.status) {
    return null;
  }
  return cur.status;
}

// shouldPoll: the bar asks the gateway about the CI only while the gateway is following it.
export function shouldPoll(w: PRWatch | null | undefined): boolean {
  return (
    w?.status === "following" ||
    w?.status === "fixing" ||
    w?.status === "reviewing" ||
    w?.status === "resolving"
  );
}

export function ciSummary(v: PRView): { passed: number; total: number } {
  const checks = v.ci?.checks ?? [];
  return {
    passed: checks.filter((c) => c.state === "success").length,
    total: checks.length,
  };
}

// shouldDesktopNotify: a browser notification is for when the user cannot see the toast, because the
// tab is in the background, and only when they allowed it.
export function shouldDesktopNotify(
  hidden: boolean,
  permission: string | undefined,
  enabled = true,
): boolean {
  return enabled && hidden && permission === "granted";
}

// wantsDesktopPermission: it is asked for once, from a click, and never again once answered.
export function wantsDesktopPermission(
  permission: string | undefined,
): boolean {
  return permission === "default";
}

export type ToastType = "success" | "error" | "warning";

// worstToast is the most serious of several toasts: when a handful of pull requests move at once the
// one toast that stands for them is as loud as the loudest.
export function worstToast(types: ToastType[]): ToastType {
  if (types.includes("error")) {
    return "error";
  }
  if (types.includes("warning")) {
    return "warning";
  }
  return "success";
}
