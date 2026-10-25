// The pull request overview: the client of GET /v1/pull-requests and the words for what it says.
// No value imports: the node tests load this file as is.
import type { Api, CICheck, CIState, MergeCode, PRRef, PRWatch } from "./prApi";

// PRItem is one open pull request of a project's repository.
export interface PRItem {
  project_id: string;
  project: string;
  repo: string;
  pr: PRRef & { head?: string; base?: string };
  ci?: { state: CIState; checks: CICheck[] };
  merge?: { code: MergeCode };
  // session follows the pull request; empty when none does.
  session?: string;
  watch?: PRWatch;
}

// PRProblem is a project whose pull requests could not be read.
export interface PRProblem {
  project: string;
  error: string;
}

export interface PRList {
  pull_requests: PRItem[];
  problems?: PRProblem[];
}

// fetchPullRequests reads the overview; refresh skips the gateway's short memory of it.
export async function fetchPullRequests(
  api: Api,
  refresh = false,
): Promise<PRList> {
  const res = await api("/v1/pull-requests" + (refresh ? "?refresh=1" : ""));
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || "HTTP " + res.status);
  }
  return res.json();
}

export type Tone = "good" | "bad" | "wait" | "idle";

// ciBadge says how the CI stands: the word, how many checks passed, and how loud to say it.
export function ciBadge(item: PRItem): {
  label: string;
  detail: string;
  tone: Tone;
} {
  const ci = item.ci;
  if (!ci) {
    return { label: "none", detail: "", tone: "idle" };
  }
  const passed = ci.checks.filter((c) => c.state === "success").length;
  const detail = ci.checks.length > 0 ? passed + "/" + ci.checks.length : "";
  const tones: Record<CIState, Tone> = {
    success: "good",
    failure: "bad",
    pending: "wait",
    none: "idle",
  };
  return { label: ci.state, detail, tone: tones[ci.state] };
}

// mergeNote is what keeps a pull request from merging, when the host says: a branch that is behind
// or conflicts is what the person can act on.
export function mergeNote(item: PRItem): MergeCode | null {
  const code = item.merge?.code;
  return code && code !== "ok" && code !== "unknown" ? code : null;
}

// failingFirst puts the pull requests that need a look before the rest: a failing CI, then a
// conflict, then the ones still running. The order inside a group is the host's.
export function failingFirst(items: PRItem[]): PRItem[] {
  const rank = (i: PRItem) => {
    if (i.ci?.state === "failure") {
      return 0;
    }
    if (i.merge?.code === "conflict") {
      return 1;
    }
    return i.ci?.state === "pending" ? 2 : 3;
  };
  return items
    .map((item, at) => ({ item, at }))
    .sort((a, b) => rank(a.item) - rank(b.item) || a.at - b.at)
    .map((e) => e.item);
}
