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
