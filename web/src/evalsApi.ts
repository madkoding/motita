// Client for the gateway's model check (/v1/evals), plus the pure logic the settings draw from it.
// No value imports besides the error class: the node tests load this file as is.
import { type Api, LLMError } from "./llmApi";

export interface EvalCaseResult {
  id: string;
  pass: boolean;
  steps: number;
  tokens: number;
  cost_usd: number;
  cost_known: boolean;
  reason?: string;
  error?: string;
}

export interface EvalSummary {
  passed: number;
  total: number;
  steps: number;
  tokens: number;
  cost_usd: number;
  cost_known: boolean;
}

export interface EvalRun {
  id: string;
  at: string;
  provider: string;
  model: string;
  results: EvalCaseResult[];
  summary: EvalSummary;
}

export interface EvalVerdict {
  // first | better | same | worse
  status: string;
  delta_passed: number;
  delta_steps: number;
  delta_cost_usd: number;
  regressed: string[];
  fixed: string[];
  steps_up: boolean;
  cost_up: boolean;
}

export interface EvalProgress {
  running: boolean;
  done: number;
  total: number;
  next?: string;
  error?: string;
  stopped?: boolean;
}

export interface EvalHistory {
  id: string;
  at: string;
  provider: string;
  model: string;
  summary: EvalSummary;
}

export interface EvalView {
  supported: boolean;
  cases: { id: string }[];
  provider: string;
  model: string;
  // Why the last run does not answer "is it still fine?": never, model, prompt, cases; "" when it does.
  stale: string;
  progress: EvalProgress;
  last: EvalRun | null;
  verdict: EvalVerdict | null;
  history: EvalHistory[];
}

const JSON_HEADERS = { "Content-Type": "application/json" };

async function send(api: Api, path: string, init?: RequestInit) {
  const res = await api(path, init);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new LLMError(
      body.error || "HTTP " + res.status,
      res.status,
      body.code,
    );
  }
  return body;
}

export function loadEvals(api: Api): Promise<EvalView> {
  return send(api, "/v1/evals");
}

// runEvals starts a check. The gateway refuses one that is not confirmed, so the page asks first.
export function runEvals(api: Api): Promise<EvalProgress> {
  return send(api, "/v1/evals/run", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ confirm: true }),
  });
}

export function stopEvals(api: Api): Promise<EvalProgress> {
  return send(api, "/v1/evals/stop", { method: "POST" });
}

// costText is an approximate price, shown with two decimals and "less than a cent" below one.
export function costText(usd: number): string {
  if (usd > 0 && usd < 0.01) {
    return "<$0.01";
  }
  return "$" + usd.toFixed(2);
}

// signed puts a plus sign on an increase so a change reads as a change.
export function signed(n: number): string {
  if (n > 0) {
    return "+" + n;
  }
  return String(n);
}
