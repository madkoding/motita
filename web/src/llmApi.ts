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
  // How capable it is (basic, mid, frontier), what it costs relative to the others (local, low,
  // mid, high) and its approximate USD price per million input and output tokens. All optional:
  // a model the catalogue does not know carries none, and none is ever guessed.
  tier?: string;
  cost?: string;
  price_in?: number;
  price_out?: number;
  // True when tier and cost were guessed from the model's name, not read from the catalogue.
  estimated?: boolean;
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
  // The models the provider's last test listed: the ones it really serves this account.
  listed_models?: string[];
  fetch_models?: boolean;
  default: boolean;
}

// A model assigned to a kind of work. A kind with none follows the default.
export interface ModelChoice {
  provider: string;
  model: string;
}

// The model of the custom provider and the limits it runs with. A missing temperature is the
// endpoint's own default; a zero size keeps the general one.
export interface CustomModel {
  model: string;
  temperature?: number;
  max_tokens: number;
  context_window: number;
}

export interface LLMView {
  provider: string;
  model: string;
  writable: boolean;
  providers: LLMProvider[];
  // The kinds of work a model can be assigned to, in the order to show them, and the assignments.
  task_kinds?: string[];
  tasks?: Record<string, ModelChoice>;
  // How hard each kind of work thinks (an absent kind follows the session's level), and the levels.
  thinking?: Record<string, string>;
  thinking_levels?: string[];
  custom_model?: CustomModel;
  // The day the catalogue's prices were last compared with the vendors' pages.
  prices_checked?: string;
}

// What one provider has consumed since the gateway started, as it reported in its responses, and the
// limits it sent with its latest answer. A quota appears only when the provider reports one.
export interface ProviderUsage {
  id: string;
  name: string;
  default: boolean;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  calls: number;
  // When the count began and when the latest call that reported usage finished (ISO 8601); absent
  // before the first call.
  since?: string;
  last?: string;
  // What the provider reported about its limits with its latest answer: requests and tokens per
  // minute, and the plan's session and weekly quotas.
  limits?: {
    requests?: UsageWindow;
    tokens?: UsageWindow;
    quotas?: UsageQuota[];
  };
}

// A plan's allowance over a window: the share used, how long the window is and when it starts over
// (ISO 8601). "session" is the short window (hours), "week" the weekly one and "month" a monthly one (Copilot's premium requests).
export interface UsageQuota {
  name: "session" | "week" | "month";
  used_percent: number;
  // Set instead of a share when the provider only says where the usage stands: below its warning
  // level ("ok") or past it ("warning"), as a Claude subscription does until a threshold nears.
  status?: "ok" | "warning";
  window_minutes?: number;
  resets_at?: string;
}

export interface UsageWindow {
  limit: number;
  remaining: number;
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

// What the custom model form holds: everything as typed.
export interface CustomDraft {
  model: string;
  temperature: string;
  max_tokens: string;
  context_window: string;
}

// customDraft is the form's text for a saved custom model.
export function customDraft(m: CustomModel | undefined): CustomDraft {
  return {
    model: m?.model || "",
    temperature: m?.temperature === undefined ? "" : String(m.temperature),
    max_tokens: m?.max_tokens ? String(m.max_tokens) : "",
    context_window: m?.context_window ? String(m.context_window) : "",
  };
}

// numberOf reads a typed number: undefined when the field is empty, NaN when it is not a number.
function numberOf(raw: string): number | undefined {
  const text = raw.trim();
  return text === "" ? undefined : Number(text);
}

// customProblem says what is wrong with the typed limits, or "" when they can be saved: the
// temperature a number from 0 to 2, the sizes whole numbers above zero.
export function customProblem(d: CustomDraft): string {
  const temperature = numberOf(d.temperature);
  if (temperature !== undefined && !(temperature >= 0 && temperature <= 2)) {
    return "temperature";
  }
  for (const key of ["max_tokens", "context_window"] as const) {
    const n = numberOf(d[key]);
    if (n !== undefined && !(Number.isInteger(n) && n > 0)) {
      return key;
    }
  }
  return "";
}

// customBody is what a save sends: the model trimmed and the limits as numbers, an empty one as
// the default (no temperature, size zero).
export function customBody(d: CustomDraft): CustomModel {
  return {
    model: d.model.trim(),
    temperature: numberOf(d.temperature),
    max_tokens: numberOf(d.max_tokens) ?? 0,
    context_window: numberOf(d.context_window) ?? 0,
  };
}

// sameCustom says whether the form holds what is saved already.
export function sameCustom(
  d: CustomDraft,
  m: CustomModel | undefined,
): boolean {
  const saved = customDraft(m);
  return (
    d.model.trim() === saved.model &&
    numberOf(d.temperature) === numberOf(saved.temperature) &&
    numberOf(d.max_tokens) === numberOf(saved.max_tokens) &&
    numberOf(d.context_window) === numberOf(saved.context_window)
  );
}

export function saveCustomModel(api: Api, m: CustomModel): Promise<LLMView> {
  return send<LLMView>(api, "/v1/llm/custom-model", {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify(m),
  });
}

// saveTasks replaces the model of every kind of work; a kind left out follows the default. With
// `thinking` it replaces the level of every kind too; without it the saved levels stay.
export function saveTasks(
  api: Api,
  tasks: Record<string, ModelChoice>,
  thinking?: Record<string, string>,
): Promise<LLMView> {
  return send<LLMView>(api, "/v1/llm/tasks", {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify({ tasks, thinking }),
  });
}

// taskChoices is what a save of the kinds sends: the kinds with a provider and a model, trimmed.
export function taskChoices(
  draft: Record<string, ModelChoice>,
): Record<string, ModelChoice> {
  const out: Record<string, ModelChoice> = {};
  for (const [kind, c] of Object.entries(draft)) {
    const model = c.model.trim();
    if (c.provider && model) {
      out[kind] = { provider: c.provider, model };
    }
  }
  return out;
}

// sameTasks says whether two sets of assignments are the same, the empty ones aside.
export function sameTasks(
  a: Record<string, ModelChoice>,
  b: Record<string, ModelChoice>,
): boolean {
  const x = taskChoices(a);
  const y = taskChoices(b);
  const keys = new Set([...Object.keys(x), ...Object.keys(y)]);
  for (const k of keys) {
    if (x[k]?.provider !== y[k]?.provider || x[k]?.model !== y[k]?.model) {
      return false;
    }
  }
  return true;
}

// sameThinking says whether two sets of levels are the same, the empty ones aside.
export function sameThinking(
  a: Record<string, string>,
  b: Record<string, string>,
): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const k of keys) {
    if ((a[k] || "") !== (b[k] || "")) {
      return false;
    }
  }
  return true;
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

export interface ModelOption {
  id: string;
  label: string;
  tier?: string;
  cost?: string;
  price_in?: number;
  price_out?: number;
  estimated?: boolean;
}

// modelOptions are the models a provider offers, by name where its catalogue gives one: the ones
// its last test listed, else its catalogue. The catalogue's tier, cost and prices ride along.
export function modelOptions(p: LLMProvider | undefined): ModelOption[] {
  if (!p) {
    return [];
  }
  const known = (id: string) => p.models.find((m) => m.id === id);
  let ids = p.models.map((m) => m.id);
  if (p.listed_models?.length) {
    ids = p.listed_models;
  }
  return ids.map((id) => {
    const m = known(id);
    const facts: Partial<ModelOption> = {};
    for (const k of [
      "tier",
      "cost",
      "price_in",
      "price_out",
      "estimated",
    ] as const) {
      if (m?.[k] !== undefined) {
        Object.assign(facts, { [k]: m[k] });
      }
    }
    return { id, label: m?.label || id, ...facts };
  });
}

// Cost and capability in plain words, with the number of marks (out of 3) that draw them. The
// words are English here; the page translates them.
// The most marks a meter draws.
const TOP = 3;
const COST: Record<string, [string, number]> = {
  local: ["Free (local)", 0],
  low: ["Cheap", 1],
  mid: ["Moderate", 2],
  high: ["Expensive", TOP],
};
const TIER: Record<string, [string, number]> = {
  basic: ["Basic", 1],
  mid: ["Capable", 2],
  frontier: ["Very capable", TOP],
};

// costLabel is how much a model costs in a word ("Cheap"), or "" when unknown.
export function costLabel(cost: string | undefined): string {
  return COST[cost || ""]?.[0] || "";
}

// costLevel is 0 (free) to 3 (expensive), for a cost costLabel named.
export function costLevel(cost: string | undefined): number {
  return COST[cost as string][1];
}

// tierLabel is how capable a model is in a word ("Very capable"), or "" when unknown.
export function tierLabel(tier: string | undefined): string {
  return TIER[tier || ""]?.[0] || "";
}

// tierLevel is 1 (basic) to 3 (very capable), for a tier tierLabel named.
export function tierLevel(tier: string | undefined): number {
  return TIER[tier as string][1];
}

// priceText is "$1.25 / $10" (read / written, per million tokens), or "" without a per-token price.
export function priceText(o: ModelOption): string {
  if (!o.price_in || !o.price_out) {
    return "";
  }
  const n = (v: number) => `$${Number(v.toFixed(2))}`;
  return `${n(o.price_in)} / ${n(o.price_out)}`;
}

// getLLMUsage reads what each provider with a key has consumed, or null from a gateway that has no
// such API (404) or cannot answer: the bottom bar then simply says nothing.
export async function getLLMUsage(api: Api): Promise<ProviderUsage[] | null> {
  const res = await api("/v1/llm/usage");
  if (!res.ok) {
    return null;
  }
  const body = (await res.json()) as { providers?: ProviderUsage[] };
  return body.providers ?? [];
}

// usageTotal is every token the provider moved, cached ones included.
export function usageTotal(u: ProviderUsage): number {
  return u.input + u.output + u.cache_read + u.cache_write;
}
