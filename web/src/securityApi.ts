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
