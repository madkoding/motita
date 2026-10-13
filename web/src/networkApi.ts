// Client for the gateway's network exposure API: which addresses the gateway listens on, and
// whether Tailscale publishes it inside the tailnet. No value imports: the node tests load this
// file as is.

import type { Api } from "./gitApi";

// local: 127.0.0.1 only. lan: every interface, but only private addresses may connect. all:
// 0.0.0.0, every origin. custom: an address written by hand in the configuration.
export type NetworkMode = "local" | "lan" | "all" | "custom";

// The modes the page can choose; custom is only ever read.
export type ChosenMode = Exclude<NetworkMode, "custom">;

export interface TailscaleView {
  installed: boolean;
  running: boolean;
  state: string;
  dns_name?: string;
  serving: boolean;
  url?: string;
  error?: string;
}

export interface NetworkView {
  // Saved in the configuration file: what the next start binds.
  mode: NetworkMode;
  listen: string;
  // What this running gateway is bound to.
  active_mode: NetworkMode;
  active_listen: string;
  restart_needed: boolean;
  can_save: boolean;
  // Whether the gateway can restart itself (it runs as a service). False for the gateway embedded
  // in a terminal UI, and absent on an older gateway: then it is restarted by hand.
  can_restart?: boolean;
  tailscale?: TailscaleView;
}

const HTTP_NOT_FOUND = 404;
const JSON_HEADERS = { "Content-Type": "application/json" };
export const NETWORK_PATH = "/v1/network";
export const TAILSCALE_PATH = "/v1/network/tailscale";
export const RESTART_PATH = "/v1/network/restart";

// NetworkError carries the HTTP status next to the gateway's message.
export class NetworkError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function failure(res: Response): Promise<NetworkError> {
  const body = await res.json().catch(() => ({}));
  return new NetworkError(body?.error || "HTTP " + res.status, res.status);
}

async function view(res: Response): Promise<NetworkView> {
  if (!res.ok) {
    throw await failure(res);
  }
  return (await res.json()) as NetworkView;
}

// getNetwork reads the network view, or null from a gateway that has no network API (404).
export async function getNetwork(api: Api): Promise<NetworkView | null> {
  const res = await api(NETWORK_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  return view(res);
}

// setNetworkMode saves the mode the next start binds. The page only calls it after the person
// confirmed what it implies, and says so: the gateway refuses a change without confirm.
export async function setNetworkMode(
  api: Api,
  mode: ChosenMode,
): Promise<NetworkView> {
  const res = await api(NETWORK_PATH, {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify({ mode, confirm: true }),
  });
  return view(res);
}

// setTailscale turns `tailscale serve` for this gateway on or off.
export async function setTailscale(
  api: Api,
  enable: boolean,
): Promise<NetworkView> {
  const res = await api(TAILSCALE_PATH, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ enable }),
  });
  return view(res);
}

// restartGateway asks the gateway to restart itself so the saved exposure applies. It answers 202
// with the network view, then the process restarts a moment later. The page only calls it after
// the person confirmed, and says so: the gateway refuses a restart without confirm.
export async function restartGateway(api: Api): Promise<NetworkView> {
  const res = await api(RESTART_PATH, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ confirm: true }),
  });
  return view(res);
}
