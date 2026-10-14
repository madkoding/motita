// Client for the gateway's LLM provider API (/v1/llm), plus the pure logic the settings draw
// from it. No value imports: the node tests load this file as is.
//
// A key is sent to the gateway and never comes back: a provider only says where its key comes
// from (KeySource), so the page can say "saved" without ever holding the secret.

export type Api = (path: string, init?: RequestInit) => Promise<Response>;

// Where a provider's key comes from, as the gateway reports it.
export type KeySource =
  | "env"
  | "stored"
  | "config"
  | "login"
  | "not_needed"
  | "missing";

export interface LLMModel {
  id: string;
  label: string;
  note?: string;
}

export interface LLMProvider {
  id: string;
  name: string;
  access: string;
  console_url?: string;
  key: KeySource;
  key_variable?: string;
  key_hint?: string;
  login?: string;
  supports_login?: boolean;
  ask_base_url?: boolean;
  base_url?: string;
  default_base_url?: string;
  models: LLMModel[];
  fetch_models?: boolean;
  default: boolean;
}

export interface LLMView {
  provider: string;
  model: string;
  writable: boolean;
  providers: LLMProvider[];
}

export interface TestResult {
  ok: boolean;
  models?: string[];
  error?: string;
}

// LLMError carries the HTTP status and the machine code the gateway answered with.
export class LLMError extends Error {
  status: number;
  code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

const JSON_HEADERS = { "Content-Type": "application/json" };

async function failure(res: Response): Promise<LLMError> {
  const body = await res.json().catch(() => ({}));
  return new LLMError(
    body.error || "HTTP " + res.status,
    res.status,
    body.code,
  );
}

async function send<T>(api: Api, path: string, init?: RequestInit): Promise<T> {
  const res = await api(path, init);
  if (!res.ok) {
    throw await failure(res);
  }
  return res.json();
}

function providerPath(id: string): string {
  return "/v1/llm/providers/" + encodeURIComponent(id);
}

export function loadLLM(api: Api): Promise<LLMView> {
  return send<LLMView>(api, "/v1/llm");
}

// saveProvider stores a key and/or an endpoint. A field left undefined is left as it is.
export function saveProvider(
  api: Api,
  id: string,
  change: { api_key?: string; base_url?: string },
): Promise<LLMProvider> {
  return send<LLMProvider>(api, providerPath(id), {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify(change),
  });
}

export function forgetProvider(api: Api, id: string): Promise<LLMProvider> {
  return send<LLMProvider>(api, providerPath(id), { method: "DELETE" });
}

export function testProvider(
  api: Api,
  id: string,
  trial: { api_key?: string; base_url?: string },
): Promise<TestResult> {
  return send<TestResult>(api, providerPath(id) + "/test", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(trial),
  });
}

export function saveDefault(
  api: Api,
  provider: string,
  model: string,
): Promise<LLMView> {
  return send<LLMView>(api, "/v1/llm/default", {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify({ provider, model }),
  });
}

// A sign-in with the provider's account, run by the gateway. The page only learns its state.
export interface LoginView {
  id: string;
  provider: string;
  method: "device" | "code" | "instant";
  url?: string;
  code?: string;
  status: "pending" | "connected" | "failed";
  error?: string;
}

function loginPath(flow: string): string {
  return "/v1/llm/logins/" + encodeURIComponent(flow);
}

export function startLogin(api: Api, id: string): Promise<LoginView> {
  return send<LoginView>(api, providerPath(id) + "/login", { method: "POST" });
}

export function loginStatus(api: Api, flow: string): Promise<LoginView> {
  return send<LoginView>(api, loginPath(flow));
}

// pasteLogin hands a code login the address the browser ended on.
export async function pasteLogin(
  api: Api,
  flow: string,
  code: string,
): Promise<void> {
  const res = await api(loginPath(flow) + "/paste", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ code }),
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

export async function cancelLogin(api: Api, flow: string): Promise<void> {
  await api(loginPath(flow), { method: "DELETE" }).catch(() => undefined);
}

// isConnected says whether a provider can run: it has a key, a login, or needs neither.
export function isConnected(p: LLMProvider): boolean {
  return p.key !== "missing";
}

// canTypeKey says whether the settings may take a key for this provider: not when it needs none,
// and not when an exported variable or the configuration file holds it, since those win.
export function canTypeKey(p: LLMProvider): boolean {
  return p.key !== "not_needed" && p.key !== "env" && p.key !== "config";
}

// trialOf is what a test or a save sends: the key only when one was typed, the endpoint only
// when the provider takes one and it differs from the one in force.
export function trialOf(
  p: LLMProvider,
  key: string,
  base: string,
): { api_key?: string; base_url?: string } {
  const out: { api_key?: string; base_url?: string } = {};
  if (key.trim()) {
    out.api_key = key.trim();
  }
  const b = base.trim();
  if (p.ask_base_url && b !== (p.base_url || "")) {
    out.base_url = b;
  }
  return out;
}

// modelChoices are the models a provider offers: the ones a test listed, else its catalogue,
// with the one in force kept in the list so the picker can show it.
export function modelChoices(
  p: LLMProvider | undefined,
  listed: string[] | undefined,
  current: string,
): string[] {
  let ids: string[];
  if (listed?.length) {
    ids = [...listed];
  } else if (p) {
    ids = p.models.map((m) => m.id);
  } else {
    ids = [];
  }
  if (current && !ids.includes(current)) {
    ids.unshift(current);
  }
  return ids;
}
