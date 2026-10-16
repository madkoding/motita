// Client for the gateway's folder listing, which the project dialogs browse to place a project
// outside the workspace. No value imports: the node tests load this file as is.

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
  path: string;
  parent?: string;
  home?: string;
  root: string;
  dirs: DirEntry[];
}

export const DIRS_PATH = "/v1/fs/dirs";

// listDirs reads the folders inside path, or inside the user's home when path is empty. The
// gateway only answers beyond the workspace when the whole disk is allowed in Settings → Security.
export async function listDirs(api: Api, path = ""): Promise<DirListing> {
  const query = path ? "?path=" + encodeURIComponent(path) : "";
  const res = await api(DIRS_PATH + query);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as DirListing;
}
