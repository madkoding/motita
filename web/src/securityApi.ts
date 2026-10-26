// Client for the gateway's security API: how much of the disk the agent reaches. No value
// imports: the node tests load this file as is.

import type { Api } from "./gitApi";

// workspace: the agent's file actions and commands stay inside the folder it works in (the
// default). full: they reach anywhere the user's account can.
export type DiskAccess = "workspace" | "full";

export interface SecurityView {
  disk_access: DiskAccess;
  // Whether there is a configuration file to save a change in.
  can_save: boolean;
  // Whether the kernel confines the agent's commands too (Linux with Landlock). Without it the
  // workspace limit holds for motita's own file actions only.
  enforced: boolean;
  // Whether the agent asks before the project's own code runs (a script, a build, a test) where
  // nothing confines its commands. Missing from a gateway older than the setting.
  ask_unconfined_runs?: boolean;
  // Whether the agent's commands run without a network, and whether this machine can do it.
  // Missing from a gateway older than the setting.
  isolate_network?: boolean;
  network_enforced?: boolean;
  // Why this machine cannot, and what would make it possible (a command to run once), when it
  // cannot. Missing where it can, and from an older gateway.
  network_reason?: string;
  network_hint?: string;
  // Whether the suggestions of a finished task are applied without asking. Missing from a gateway
  // older than the setting.
  auto_apply_suggestions?: boolean;
  // Whether every command the policy would ask about runs without asking. Missing from a gateway
  // older than the setting.
  approve_all_commands?: boolean;
  // Whether the agent may see this computer's screen and use its mouse, keyboard and windows,
  // asking before every step, and why there is no desktop here when there is none. Missing from a
  // gateway older than the setting.
  desktop_control?: boolean;
  desktop_reason?: string;
  // Whether the gateway itself runs with NO_NEW_PRIVS (a systemd unit with NoNewPrivileges=yes, a
  // container with no-new-privileges), which every command inherits: sudo cannot work, approved or
  // not. Missing where it does not, and from an older gateway.
  no_new_privs?: boolean;
  no_new_privs_hint?: string;
}

const HTTP_NOT_FOUND = 404;
export const SECURITY_PATH = "/v1/security";

async function view(res: Response): Promise<SecurityView> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as SecurityView;
}

// getSecurity reads the security view, or null from a gateway that has no security API (404).
export async function getSecurity(api: Api): Promise<SecurityView | null> {
  const res = await api(SECURITY_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  return view(res);
}

// setDiskAccess saves how much of the disk the agent reaches. The page only widens it to the whole
// disk after the person confirmed what it implies, and says so: the gateway refuses that change
// without confirm. Going back to the workspace needs none.
export async function setDiskAccess(
  api: Api,
  mode: DiskAccess,
): Promise<SecurityView> {
  const res = await api(SECURITY_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ disk_access: mode, confirm: mode === "full" }),
  });
  return view(res);
}

// setAskUnconfinedRuns saves whether the agent asks before the project's own code runs where
// nothing confines its commands. Turning the question off is sent only after the person confirmed
// what it implies, with confirm; turning it back on needs none.
export async function setAskUnconfinedRuns(
  api: Api,
  ask: boolean,
): Promise<SecurityView> {
  const res = await api(SECURITY_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ask_unconfined_runs: ask, confirm: !ask }),
  });
  return view(res);
}

// setAutoApplySuggestions saves whether the suggestions of a finished task are applied without
// asking. Turning it on is sent only after the person confirmed what it implies, with confirm;
// turning it off needs none.
export async function setAutoApplySuggestions(
  api: Api,
  on: boolean,
): Promise<SecurityView> {
  const res = await api(SECURITY_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ auto_apply_suggestions: on, confirm: on }),
  });
  return view(res);
}

// setApproveAllCommands saves whether every command runs without asking. Turning it on is sent
// only after the person confirmed what it implies, with confirm; turning it off needs none.
export async function setApproveAllCommands(
  api: Api,
  on: boolean,
): Promise<SecurityView> {
  const res = await api(SECURITY_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ approve_all_commands: on, confirm: on }),
  });
  return view(res);
}

// setDesktopControl saves whether the agent may control this computer's desktop. Turning it on is
// sent only after the person confirmed what it implies, with confirm; turning it off needs none.
export async function setDesktopControl(
  api: Api,
  on: boolean,
): Promise<SecurityView> {
  const res = await api(SECURITY_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ desktop_control: on, confirm: on }),
  });
  return view(res);
}

// setIsolateNetwork saves whether the agent's commands run without a network. Giving them the
// network is sent only after the person confirmed what it implies, with confirm; taking it back
// needs none.
export async function setIsolateNetwork(
  api: Api,
  on: boolean,
): Promise<SecurityView> {
  const res = await api(SECURITY_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ isolate_network: on, confirm: !on }),
  });
  return view(res);
}

// SessionScope is where a session's commands work and what the sandbox keeps from them.
export interface SessionScope {
  workspace: string;
  // The project's checkout, "" for a session of no project.
  project_dir: string;
  // The HOME its commands get, "" when the sandbox has no tools directory.
  home: string;
  // The folders its commands may not read beyond the ones they work in.
  private_roots: string[];
}

// getSessionScope reads a session's scope, or null when there is no session or the gateway has
// no such route.
export async function getSessionScope(
  api: Api,
  sessionId: string,
): Promise<SessionScope | null> {
  if (!sessionId) {
    return null;
  }
  const res = await api(
    "/v1/sessions/" + encodeURIComponent(sessionId) + "/scope",
  );
  if (!res.ok) {
    return null;
  }
  return (await res.json()) as SessionScope;
}
