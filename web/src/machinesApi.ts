// Client for the gateway's other computers: this one, the ones found on the network and the ones
// connected with their token, and the path that reaches a connected one's API through this
// gateway. No value imports: the node tests load this file as is.

import type { Api } from "./gitApi";

// How a computer was found.
export type MachineSource = "lan" | "tailscale" | "manual";

// Machine is another computer running a gateway.
export interface Machine {
  id: string;
  // What it calls itself; alias is what it was renamed to here.
  name: string;
  alias?: string;
  address: string;
  source: MachineSource;
  version?: string;
  // Connected with its token: its sessions can be opened from here.
  paired: boolean;
  online: boolean;
}

// SelfMachine is this computer.
export interface SelfMachine {
  id: string;
  name: string;
  hostname: string;
  // False when the gateway listens on this computer only: no other one can connect to it.
  reachable: boolean;
}

export interface MachinesView {
  self: SelfMachine;
  peers: Machine[];
  discovery: boolean;
}

export const PEERS_PATH = "/v1/peers";
const JSON_HEADERS = { "Content-Type": "application/json" };
const API_PREFIX = "/v1/";

// proxyPath is where a path of a connected computer's API is reached through this gateway; this
// computer's own paths ("" id) and anything outside the API stay as they are.
export function proxyPath(id: string, path: string): string {
  if (!id || !path.startsWith(API_PREFIX)) {
    return path;
  }
  return PEERS_PATH + "/" + encodeURIComponent(id) + "/proxy" + path;
}

// machineApi is api pointed at one computer: every request of the page goes to it.
export function machineApi(api: Api, id: string): Api {
  if (!id) {
    return api;
  }
  return (path, init) => api(proxyPath(id, path), init);
}

// machineName is the name a computer is shown under.
export function machineName(m: Pick<Machine, "name" | "alias">): string {
  return m.alias || m.name;
}

async function view(res: Response): Promise<MachinesView> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as MachinesView;
}

function send(api: Api, method: string, path: string, body?: object) {
  return api(path, {
    method,
    headers: JSON_HEADERS,
    body: body && JSON.stringify(body),
  }).then(view);
}

// getMachines reads this computer and the others.
export function getMachines(api: Api): Promise<MachinesView> {
  return api(PEERS_PATH).then(view);
}

// renameSelf sets the name this computer is shown and announced under; "" is its host name.
export function renameSelf(api: Api, name: string): Promise<MachinesView> {
  return send(api, "PUT", PEERS_PATH + "/self", { name });
}

// renameMachine sets the name a connected computer is shown under here; "" is its own name.
export function renameMachine(
  api: Api,
  id: string,
  alias: string,
): Promise<MachinesView> {
  return send(api, "PUT", PEERS_PATH + "/" + encodeURIComponent(id), {
    alias,
  });
}

// connectMachine connects a found computer with its token.
export function connectMachine(
  api: Api,
  id: string,
  token: string,
): Promise<MachinesView> {
  return send(api, "PUT", PEERS_PATH + "/" + encodeURIComponent(id), {
    token,
  });
}

// addMachine connects a computer by its address and token (one across a VPN, say).
export function addMachine(
  api: Api,
  address: string,
  token: string,
): Promise<MachinesView> {
  return send(api, "POST", PEERS_PATH, { address, token });
}

// forgetMachine disconnects a computer and drops its token here.
export function forgetMachine(api: Api, id: string): Promise<MachinesView> {
  return send(api, "DELETE", PEERS_PATH + "/" + encodeURIComponent(id));
}
