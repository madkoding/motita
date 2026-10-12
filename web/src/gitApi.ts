// Client for the gateway's git-host connection API, plus the pure logic around it (flow state
// machine, polling, repo-name derivation). No value imports: the node tests load this file as is.

export type Api = (path: string, init?: RequestInit) => Promise<Response>;

export type GitMethod = "device" | "code" | "token";

export interface GitAccount {
  id: string;
  name: string;
  kind: string;
  host: string;
  connected: boolean;
  username?: string;
  methods: GitMethod[];
  oauth_ready: boolean;
  token_url?: string;
  setup_hint?: string;
}

export interface GitRepo {
  full_name: string;
  description?: string;
  clone_url: string;
  web_url?: string;
  private?: boolean;
  default_branch?: string;
  updated_at?: string;
}

export interface ReposPage {
  repos: GitRepo[];
  more: boolean;
  account?: string;
}

export interface FlowStart {
  flow_id: string;
  method: "device" | "code";
  service: string;
  url: string;
  code?: string;
  expires_in?: number;
  redirect_uri?: string;
}

export interface FlowStatus {
  status: "pending" | "connected" | "failed";
  service?: string;
  account?: string;
  error?: string;
}

// The HTTP statuses the gateway answers with that this client tells apart.
const HTTP_ACCEPTED = 202;
const HTTP_NO_CONTENT = 204;
const HTTP_NOT_FOUND = 404;
const HTTP_CONFLICT = 409;
const HTTP_GONE = 410;

// GitError carries the HTTP status and, for a 409, the machine code and service.
export class GitError extends Error {
  status: number;
  code?: string;
  service?: string;
  constructor(
    message: string,
    status: number,
    code?: string,
    service?: string,
  ) {
    super(message);
    this.status = status;
    this.code = code;
    this.service = service;
  }
  get authRequired(): boolean {
    return this.status === HTTP_CONFLICT && this.code === "git_auth_required";
  }
}

async function failure(res: Response): Promise<GitError> {
  const body = await res.json().catch(() => ({}));
  return new GitError(
    body.error || "HTTP " + res.status,
    res.status,
    body.code,
    body.service,
  );
}

const JSON_HEADERS = { "Content-Type": "application/json" };
const FLOWS_PATH = "/v1/git/flows/";

// listOf is the value when it is an array, an empty list otherwise.
function listOf<T>(v: unknown): T[] {
  if (Array.isArray(v)) {
    return v;
  }
  return [];
}

// messageOf is the text a failure is reported with: an Error's message, anything else as text.
function messageOf(e: unknown): string {
  if (e instanceof Error) {
    return e.message;
  }
  return String(e);
}

export async function listAccounts(api: Api): Promise<GitAccount[]> {
  const res = await api("/v1/git/accounts");
  if (!res.ok) {
    throw await failure(res);
  }
  const body = await res.json();
  return listOf<GitAccount>(body.accounts);
}

export interface ConnectRequest {
  service?: string;
  kind?: string;
  host?: string;
  method?: GitMethod;
  token?: string;
  username?: string;
}

export type ConnectResult =
  | { type: "connected"; account: string; service: string }
  | { type: "flow"; flow: FlowStart };

export async function connect(
  api: Api,
  req: ConnectRequest,
): Promise<ConnectResult> {
  const res = await api("/v1/git/connect", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    throw await failure(res);
  }
  const body = await res.json();
  if (res.status === HTTP_ACCEPTED || body.flow_id) {
    return { type: "flow", flow: body as FlowStart };
  }
  return {
    type: "connected",
    account: body.account || "",
    service: body.service || req.service || "",
  };
}

export async function flowStatus(api: Api, id: string): Promise<FlowStatus> {
  const res = await api(FLOWS_PATH + encodeURIComponent(id));
  if (!res.ok) {
    throw await failure(res);
  }
  return res.json();
}

export async function pasteCode(
  api: Api,
  id: string,
  code: string,
): Promise<void> {
  const res = await api(FLOWS_PATH + encodeURIComponent(id) + "/paste", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ code }),
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

export async function cancelFlow(api: Api, id: string): Promise<void> {
  try {
    await api(FLOWS_PATH + encodeURIComponent(id), { method: "DELETE" });
  } catch {
    /* best effort */
  }
}

export async function disconnect(api: Api, id: string): Promise<void> {
  const res = await api("/v1/git/accounts/" + encodeURIComponent(id), {
    method: "DELETE",
  });
  if (!res.ok && res.status !== HTTP_NO_CONTENT) {
    throw await failure(res);
  }
}

export async function listRepos(
  api: Api,
  service: string,
  q: string,
  page: number,
): Promise<ReposPage> {
  const qs = new URLSearchParams({ service, page: String(page) });
  if (q.trim()) {
    qs.set("q", q.trim());
  }
  const res = await api("/v1/git/repos?" + qs.toString());
  if (!res.ok) {
    throw await failure(res);
  }
  const body = await res.json();
  return {
    repos: listOf<GitRepo>(body.repos),
    more: !!body.more,
    account: body.account,
  };
}

// ── Flow state machine ──────────────────────────────────────────────────────

export type FlowState =
  | { phase: "idle" }
  | { phase: "starting" }
  | { phase: "pending"; flow: FlowStart }
  | { phase: "connected"; account: string; service: string }
  | { phase: "failed"; error: string };

export type FlowEvent =
  | { type: "start" }
  | { type: "started"; flow: FlowStart }
  | { type: "connected"; account: string; service: string }
  | { type: "failed"; error: string }
  | { type: "poll"; status: FlowStatus }
  | { type: "reset" };

export function reduceFlow(s: FlowState, e: FlowEvent): FlowState {
  switch (e.type) {
    case "reset":
      return { phase: "idle" };
    case "start":
      return { phase: "starting" };
    case "started":
      return { phase: "pending", flow: e.flow };
    case "connected":
      return { phase: "connected", account: e.account, service: e.service };
    case "failed":
      return { phase: "failed", error: e.error };
    case "poll":
      return reducePoll(s, e.status);
  }
}

// reducePoll applies a polled status to the flow: only a pending flow moves on.
function reducePoll(s: FlowState, st: FlowStatus): FlowState {
  if (s.phase !== "pending") {
    return s;
  } // a late answer after a terminal state changes nothing
  if (st.status === "connected") {
    return {
      phase: "connected",
      account: st.account || "",
      service: st.service || s.flow.service,
    };
  }
  if (st.status === "failed") {
    return {
      phase: "failed",
      error: st.error || "The host did not authorize the login.",
    };
  }
  return s;
}

export interface PollOptions {
  get: () => Promise<FlowStatus>;
  sleep: (ms: number) => Promise<void>;
  cancelled: () => boolean;
  intervalMs?: number;
  maxErrors?: number;
}

// pollFlow asks for the flow's status until it is terminal or cancelled. A few network errors in a
// row are tolerated (a flaky connection must not kill a login the user is completing in a browser).
// Returns the terminal status, or null when cancelled.
const POLL_MAX_ERRORS = 5;
const POLL_INTERVAL_MS = 2000;

export async function pollFlow(o: PollOptions): Promise<FlowStatus | null> {
  const max = o.maxErrors ?? POLL_MAX_ERRORS;
  let errors = 0;
  while (!o.cancelled()) {
    try {
      const st = await o.get();
      errors = 0;
      if (st.status !== "pending") {
        return unlessCancelled(o, st);
      }
    } catch (e) {
      if (o.cancelled()) {
        return null;
      }
      if (isExpired(e)) {
        return { status: "failed", error: "The login expired. Try again." };
      }
      errors++;
      if (errors >= max) {
        return { status: "failed", error: messageOf(e) };
      }
    }
    await o.sleep(o.intervalMs ?? POLL_INTERVAL_MS);
  }
  return null;
}

// unlessCancelled is the terminal status, or null when the poll was cancelled while it was asked.
function unlessCancelled(o: PollOptions, st: FlowStatus): FlowStatus | null {
  if (o.cancelled()) {
    return null;
  }
  return st;
}

// isExpired tells a flow the gateway no longer knows (gone, or never there) from a passing error.
function isExpired(e: unknown): boolean {
  return (
    e instanceof GitError &&
    (e.status === HTTP_NOT_FOUND || e.status === HTTP_GONE)
  );
}

// ── Pure helpers ────────────────────────────────────────────────────────────

// trimTrailingSlashes drops every `/` at the end of a string. A loop rather than /\/+$/, which
// backtracks over each run of slashes that is not at the end.
export function trimTrailingSlashes(s: string): string {
  let end = s.length;
  while (end > 0 && s[end - 1] === "/") {
    end--;
  }
  return s.slice(0, end);
}

// repoNameFromUrl: https://host/o/repo.git, git@host:o/repo.git, https://host/group/sub/repo -> repo
export function repoNameFromUrl(url: string): string {
  const m = /[/:]([^/:]+?)(?:\.git)?$/.exec(trimTrailingSlashes(url.trim()));
  if (m) {
    return m[1];
  }
  return "";
}

export function repoShortName(r: GitRepo): string {
  return repoNameFromUrl(r.clone_url) || r.full_name.split("/").pop() || "";
}

export function connectedAccounts(accts: GitAccount[]): GitAccount[] {
  return accts.filter((a) => a.connected);
}

// mergeRepos appends a page to the list without duplicating a repo the gateway repeated.
export function mergeRepos(have: GitRepo[], more: GitRepo[]): GitRepo[] {
  const seen = new Set(have.map((r) => r.clone_url));
  return have.concat(more.filter((r) => !seen.has(r.clone_url)));
}

// primaryMethod: one-click OAuth when the gateway can do it, otherwise the token.
export function primaryMethod(a: GitAccount): GitMethod {
  const oauth = a.methods.find((m) => m === "device" || m === "code");
  if (a.oauth_ready && oauth) {
    return oauth;
  }
  return "token";
}

export type RelUnit = "now" | "minute" | "hour" | "day" | "month" | "year";
export type RelTime = { unit: RelUnit; n: number };

// Lengths of time in seconds, and the days a month and a year are counted as.
const MINUTE_S = 60;
const HOUR_S = 3600;
const DAY_S = 86400;
const MONTH_DAYS = 30;
const YEAR_DAYS = 365;
const MS_PER_S = 1000;

export function relativeTime(
  iso: string | undefined,
  now: number = Date.now(),
): RelTime | null {
  if (!iso) {
    return null;
  }
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return null;
  }
  const s = Math.max(0, Math.floor((now - then) / MS_PER_S));
  if (s < MINUTE_S) {
    return { unit: "now", n: 0 };
  }
  if (s < HOUR_S) {
    return { unit: "minute", n: Math.floor(s / MINUTE_S) };
  }
  if (s < DAY_S) {
    return { unit: "hour", n: Math.floor(s / HOUR_S) };
  }
  if (s < DAY_S * MONTH_DAYS) {
    return { unit: "day", n: Math.floor(s / DAY_S) };
  }
  if (s < DAY_S * YEAR_DAYS) {
    return { unit: "month", n: Math.floor(s / (DAY_S * MONTH_DAYS)) };
  }
  return { unit: "year", n: Math.floor(s / (DAY_S * YEAR_DAYS)) };
}
