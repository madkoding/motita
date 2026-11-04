// Client for Motita's own local model (/v1/local-model): the GPU it found, what is installed, the
// download in progress and the llama-server process. Every change is a button the user pressed;
// reading the status never downloads or starts anything. No value imports: the node tests load
// this file as is.

import type { Api } from "./llmApi";

export interface LocalGPU {
  name: string;
  vendor: string;
  total_mb: number;
  free_mb?: number;
  unified?: boolean;
  source: string;
}

export interface LocalTask {
  running: boolean;
  phase?: "runtime" | "model";
  done?: number;
  total?: number;
  error?: string;
}

export interface LocalServer {
  running: boolean;
  starting?: boolean;
  port?: number;
  context?: number;
  gpu_layers?: number;
  error?: string;
}

export interface LocalStatus {
  model_id: string;
  model_name: string;
  repo: string;
  file: string;
  size: number;
  dir: string;
  gpu?: LocalGPU;
  min_vram_mb: number;
  eligible: boolean;
  reason?: "no_gpu" | "low_vram";
  model_present: boolean;
  runtime_present: boolean;
  runtime_source?: "path" | "installed";
  runtime_tag?: string;
  task: LocalTask;
  server: LocalServer;
}

// LOCAL_PROVIDER is the provider id the local model runs as.
export const LOCAL_PROVIDER = "local";

const BASE = "/v1/local-model";

async function call(
  api: Api,
  path: string,
  method = "GET",
): Promise<LocalStatus> {
  const res = await api(BASE + path, { method });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || "HTTP " + res.status);
  }
  return body;
}

export const localStatus = (api: Api) => call(api, "");
export const localInstall = (api: Api) => call(api, "/install", "POST");
export const localCancel = (api: Api) => call(api, "/cancel", "POST");
export const localStart = (api: Api) => call(api, "/start", "POST");
export const localStop = (api: Api) => call(api, "/stop", "POST");
export const localDelete = (api: Api) => call(api, "", "DELETE");

// busy says the status changes on its own (a download, a model loading), so the page reads it again.
export function busy(s: LocalStatus): boolean {
  return s.task.running || !!s.server.starting;
}

// installed says both the runtime and the model are in place.
export function installed(s: LocalStatus): boolean {
  return s.model_present && s.runtime_present;
}

// removable says Delete has something of Motita's to remove.
export function removable(s: LocalStatus): boolean {
  return s.model_present || s.runtime_source === "installed";
}

// gigabytes is a byte count as "3.3 GB".
export function gigabytes(bytes: number): string {
  return (bytes / 1e9).toFixed(1) + " GB";
}

// vram is MiB of GPU memory as "4.0 GB".
export function vram(mb: number): string {
  return (mb / 1024).toFixed(1) + " GB";
}

// percent is how far a task is, 0 when its size is not known yet.
export function percent(t: LocalTask): number {
  if (!t.total) {
    return 0;
  }
  return Math.min(100, Math.floor(((t.done || 0) * 100) / t.total));
}

// serverProblem is the server's last error worth showing: a stop the user asked for is not one.
export function serverProblem(s: LocalServer): string {
  if (!s.error || s.error === "stopped") {
    return "";
  }
  return s.error;
}
