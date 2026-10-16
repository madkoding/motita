// Client for the gateway's folder listing, which the project dialogs browse to place a project
// outside the workspace. No value imports: the node tests load this file as is.

import { choose } from "./choose";
import type { Api } from "./gitApi";

// DirEntry is one sub-folder of a listing; git marks a folder that is a repository.
export interface DirEntry {
  name: string;
  path: string;
  git?: boolean;
}

// DirListing is a folder, the one above it ("" at the top of what may be browsed), the user's
// home when it can be reached, and the folders inside.
export interface DirListing {
  // Empty for the list of the machine's drives, where nothing can be chosen.
  path: string;
  parent?: string;
  home?: string;
  root: string;
  // The folder holds the system's own files: a project there needs a second thought.
  sensitive?: boolean;
  dirs: DirEntry[];
}

export const DIRS_PATH = "/v1/fs/dirs";

// listDirs reads the folders inside path, or inside the user's home when path is empty. The
// gateway only answers beyond the workspace when the whole disk is allowed in Settings → Security.
export async function listDirs(api: Api, path = ""): Promise<DirListing> {
  const query = choose(path, "?path=" + encodeURIComponent(path), "");
  const res = await api(DIRS_PATH + query);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as DirListing;
}

// makeDir creates a folder called name inside path, and answers it.
export async function makeDir(
  api: Api,
  path: string,
  name: string,
): Promise<DirEntry> {
  const res = await api(DIRS_PATH, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path, name }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as DirEntry;
}
