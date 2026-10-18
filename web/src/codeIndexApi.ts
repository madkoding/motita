// Client for the gateway's code index API: the settings of the search_index tool. No value
// imports: the node tests load this file as is.

import type { Api } from "./gitApi";

// Where the vectors of the embeddings come from: an endpoint the person named, a local Ollama that
// is looked for, or nowhere.
export type EmbeddingsSource = "endpoint" | "auto" | "off";

export interface CodeIndexView {
  enabled: boolean;
  semantic: boolean;
  embeddings_auto: boolean;
  embeddings_url: string;
  embeddings_model: string;
  // Whether an embeddings key is set (in the environment), never what it is.
  embeddings_key: boolean;
  max_file_kb: number;
  max_files: number;
  embeddings: EmbeddingsSource;
  // Whether the endpoint is on this machine or its network: the text does not leave.
  endpoint_local: boolean;
  // The index files kept on disk and what they hold.
  cache_files: number;
  cache_bytes: number;
  // Whether there is a configuration file to save a change in.
  can_save: boolean;
}

// How much of a project's folder the index on disk covers: none, stale (the folder changed since it
// was built) or ready.
export type IndexState = "none" | "stale" | "ready";

export interface ProjectIndexStatus {
  state: IndexState;
  // Files the folder offers, files the index holds and the share covered (0 to 100).
  candidates: number;
  files: number;
  percent: number;
  chunks: number;
  terms: number;
  max_files: number;
  // The folder has at least max_files files, so some may be left out.
  capped: boolean;
  cache_bytes: number;
  semantic: boolean;
  embeddings: boolean;
}

// CodeIndexChange is any of the fields the page changes; confirm says the person was told that the
// text of their files goes to a remote endpoint.
export type CodeIndexChange = Partial<
  Pick<
    CodeIndexView,
    | "enabled"
    | "semantic"
    | "embeddings_auto"
    | "embeddings_url"
    | "embeddings_model"
    | "max_file_kb"
    | "max_files"
  >
> & { confirm?: boolean };

const HTTP_NOT_FOUND = 404;
export const CODE_INDEX_PATH = "/v1/code-index";

async function view(res: Response): Promise<CodeIndexView> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as CodeIndexView;
}

// getCodeIndex reads the settings, or null from a gateway that has no code index API (404).
export async function getCodeIndex(api: Api): Promise<CodeIndexView | null> {
  const res = await api(CODE_INDEX_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  return view(res);
}

// setCodeIndex saves a change and answers the settings as they are after it.
export async function setCodeIndex(
  api: Api,
  change: CodeIndexChange,
): Promise<CodeIndexView> {
  const res = await api(CODE_INDEX_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(change),
  });
  return view(res);
}

// CodeIndexCleared is what deleting the cache answers: the files removed and the bytes they held.
export interface CodeIndexCleared {
  removed: number;
  bytes: number;
}

// clearCodeIndexCache deletes the index files kept on disk; the next search builds them again.
export async function clearCodeIndexCache(api: Api): Promise<CodeIndexCleared> {
  const res = await api(CODE_INDEX_PATH + "/cache", { method: "DELETE" });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as CodeIndexCleared;
}

async function projectIndex(
  api: Api,
  id: string,
  method: "GET" | "POST",
): Promise<ProjectIndexStatus> {
  const res = await api(
    "/v1/projects/" + encodeURIComponent(id) + "/code-index",
    { method },
  );
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as ProjectIndexStatus;
}

// getProjectIndex reads how much of a project's folder the index covers.
export function getProjectIndex(api: Api, id: string) {
  return projectIndex(api, id, "GET");
}

// buildProjectIndex brings the project's index up to date and answers its status.
export function buildProjectIndex(api: Api, id: string) {
  return projectIndex(api, id, "POST");
}

// looksLocal is a guess at what the gateway will decide: an address on this machine or its network.
// A wrong "remote" only asks once too often; a wrong "local" is refused by the gateway.
export function looksLocal(raw: string): boolean {
  let host: string;
  try {
    host = new URL(raw.trim()).hostname.toLowerCase().replace(/^\[|\]$/g, "");
  } catch {
    return false;
  }
  return (
    !host.includes(".") ||
    /\.(local|lan|internal)$/.test(host) ||
    /^(127\.|10\.|192\.168\.|169\.254\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host)
  );
}
