// Client for the gateway's upkeep API: the weekly check and the draft pull requests. No value
// imports: the node tests load this file as is.

import type { Api } from "./gitApi";

// RoutineInfo says when a routine last ran, when it runs next and how the last run ended. Each
// field is empty when there is nothing to say.
export interface RoutineInfo {
  last_run: string;
  next_run: string;
  last_outcome: string;
}

export interface AutopilotView {
  weekly_audit: boolean;
  auto_pr: boolean;
  max_open_prs: number;
  // Whether there is a configuration file to save a change in.
  can_save: boolean;
  audit: RoutineInfo | null;
  fixes: RoutineInfo | null;
}

// AutopilotChange is any of the fields the page changes.
export type AutopilotChange = Partial<
  Pick<AutopilotView, "weekly_audit" | "auto_pr" | "max_open_prs">
>;

// Routine names the two routines that can be started by hand.
export type Routine = "audit" | "fixes";

const HTTP_NOT_FOUND = 404;
export const AUTOPILOT_PATH = "/v1/autopilot";

async function view(res: Response): Promise<AutopilotView> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as AutopilotView;
}

// getAutopilot reads the settings, or null from a gateway that has no upkeep API (404).
export async function getAutopilot(api: Api): Promise<AutopilotView | null> {
  const res = await api(AUTOPILOT_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  return view(res);
}

// setAutopilot saves a change and answers the settings as they are after it.
export async function setAutopilot(
  api: Api,
  change: AutopilotChange,
): Promise<AutopilotView> {
  const res = await api(AUTOPILOT_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(change),
  });
  return view(res);
}

// runAutopilot starts one routine now and answers the settings with its new state.
export async function runAutopilot(
  api: Api,
  what: Routine,
): Promise<AutopilotView> {
  const res = await api(AUTOPILOT_PATH + "/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ what }),
  });
  return view(res);
}
