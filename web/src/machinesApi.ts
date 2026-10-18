// Client for the gateway's other computers: this one, the ones found on the network, the ones
// connected (with their token, or let in by the person there), the ones asking to come into this
// one, and the path that reaches a connected one's API through this
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
  // How many of its sessions have a run in flight, for a connected computer that answers.
  running: number;
  // This computer's request to be let into it, while it waits or after it was refused.
  ask?: MachineAsk;
}

// MachineAsk is a request to be let into another computer: the code both screens show.
export interface MachineAsk {
  code: string;
  state: "waiting" | "denied";
}

// MachineRequest is another computer asking to be let into this one.
export interface MachineRequest {
  id: string;
  name: string;
  address: string;
  code: string;
}

// MachineGrant is a computer let into this one.
export interface MachineGrant {
  id: string;
  name: string;
  at: string;
}

// SelfMachine is this computer.
export interface SelfMachine {
  id: string;
  name: string;
  hostname: string;
  // False when the gateway listens on this computer only: no other one can connect to it.
  reachable: boolean;
  // How many sessions here have a run in flight.
  running: number;
}

export interface MachinesView {
  self: SelfMachine;
  peers: Machine[];
  discovery: boolean;
  requests: MachineRequest[];
  grants: MachineGrant[];
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

// askMachine asks a found computer to let this one in: the person there allows it.
export function askMachine(api: Api, id: string): Promise<MachinesView> {
  return send(api, "POST", PEERS_PATH + "/" + encodeURIComponent(id) + "/ask");
}

// answerRequest lets a computer that asked into this one, or not.
export function answerRequest(
  api: Api,
  id: string,
  allow: boolean,
): Promise<MachinesView> {
  return send(api, "POST", "/v1/peer-requests/" + encodeURIComponent(id), {
    allow,
  });
}

// revokeGrant takes back the access a computer was let in with.
export function revokeGrant(api: Api, id: string): Promise<MachinesView> {
  return send(api, "DELETE", "/v1/peer-grants/" + encodeURIComponent(id));
}

// addMachine connects a computer by its address (one across a VPN, say): with its token, or,
// with none, by asking it to let this one in.
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
