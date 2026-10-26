// The pull request overview: the client of GET /v1/pull-requests and the words for what it says.
// No value imports: the node tests load this file as is.
import { choose } from "./choose";
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
  // failing is how many have a failing CI.
  failing?: number;
}

// fetchPullRequests reads the overview; refresh skips the gateway's short memory of it.
export async function fetchPullRequests(
  api: Api,
  refresh = false,
): Promise<PRList> {
  const res = await api(
    "/v1/pull-requests" + choose(refresh, "?refresh=1", ""),
  );
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
  const detail = choose(
    ci.checks.length > 0,
    passed + "/" + ci.checks.length,
    "",
  );
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
  const quiet = !code || code === "ok" || code === "unknown";
  return choose(quiet, null, code as MergeCode);
}

// failingFirst puts the pull requests that need a look before the rest: a failing CI, then a
// conflict, then the ones still running. The order inside a group is the host's.
export function failingFirst(items: PRItem[]): PRItem[] {
  const needs = [
    (i: PRItem) => i.ci?.state === "failure",
    (i: PRItem) => i.merge?.code === "conflict",
    (i: PRItem) => i.ci?.state === "pending",
  ];
  const rank = (i: PRItem) => {
    const at = needs.findIndex((need) => need(i));
    return choose(at < 0, needs.length, at);
  };
  return items
    .map((item, at) => ({ item, at }))
    .sort((a, b) => rank(a.item) - rank(b.item) || a.at - b.at)
    .map((e) => e.item);
}

// canUpdate: the host says the branch is behind its base, which "Update branch" fixes.
export function canUpdate(item: PRItem): boolean {
  return item.merge?.code === "behind";
}

// updatePRBranch asks the host to merge the base into the pull request's branch. Nothing is
// rebased or force-pushed; the host refuses a branch that conflicts.
export async function updatePRBranch(api: Api, item: PRItem): Promise<void> {
  const res = await api("/v1/pull-requests/update", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      project_id: item.project_id,
      repo: item.repo,
      number: item.pr.number,
    }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || "HTTP " + res.status);
  }
}
