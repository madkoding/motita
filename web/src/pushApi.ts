// Client for the gateway's phone notifications (Web Push): the browser subscribes with the
// gateway's key, the gateway keeps the subscription and sends a notification when a CI ends, even
// with the page closed. No value imports: the node tests load this file as is.

import type { Api } from "./gitApi";

export const PUSH_PATH = "/v1/push";
export const PUSH_SUBSCRIPTIONS_PATH = "/v1/push/subscriptions";
export const PUSH_TEST_PATH = "/v1/push/test";
const JSON_HEADERS = { "Content-Type": "application/json" };

// Whether this browser has phone notifications on. The page reads it to leave a CI that ended to
// the push notification instead of showing a second one of its own.
export const PUSH_KEY = "motita.ui.push";

export function loadPushOn(): boolean {
  try {
    return localStorage.getItem(PUSH_KEY) === "on";
  } catch {
    return false;
  }
}

export function savePushOn(on: boolean): void {
  try {
    if (on) {
      localStorage.setItem(PUSH_KEY, "on");
    } else {
      localStorage.removeItem(PUSH_KEY);
    }
  } catch {
    /* ignore */
  }
}

// sessionFromNotice is the session a tap on a phone notification opens, from the message the
// service worker posts to the open page ({type: "motita-open", url: "/?session=<id>"}), or "".
export function sessionFromNotice(data: unknown): string {
  const m = data as { type?: unknown; url?: unknown } | null;
  if (m?.type !== "motita-open" || typeof m.url !== "string") {
    return "";
  }
  const query = m.url.split("?")[1] ?? "";
  return new URLSearchParams(query.split("#")[0]).get("session") || "";
}

export interface PushView {
  available: boolean;
  public_key?: string;
  subscribed: boolean;
}

// PushState is what the settings say about phone notifications on this device.
export type PushState =
  | "insecure" // the page is not on https or localhost: the browser hides push
  | "install" // an iPhone or iPad: only the app added to the home screen can receive them
  | "unsupported" // this browser has no push
  | "unavailable" // the gateway has nowhere to keep subscriptions (or is older than push)
  | "blocked" // the person refused notifications to this site
  | "on"
  | "off";

// pushState reads the device and the gateway into one answer, in the order that matters to the
// person: what blocks it first.
export function pushState(o: {
  secure: boolean;
  supported: boolean;
  ios: boolean;
  installed: boolean;
  gateway: PushView | null;
  permission: string | undefined;
  subscribed: boolean;
}): PushState {
  if (!o.secure) {
    return "insecure";
  }
  if (!o.supported) {
    return o.ios && !o.installed ? "install" : "unsupported";
  }
  if (!o.gateway?.available) {
    return "unavailable";
  }
  if (o.permission === "denied") {
    return "blocked";
  }
  return o.subscribed && o.gateway.subscribed ? "on" : "off";
}

// pushSupported says whether this browser can receive push at all.
export function pushSupported(): boolean {
  return (
    typeof navigator !== "undefined" &&
    "serviceWorker" in navigator &&
    typeof window !== "undefined" &&
    "PushManager" in window
  );
}

// keyBytes turns the gateway's base64url key into what PushManager.subscribe takes.
export function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

async function failure(res: Response): Promise<Error> {
  const body = await res.json().catch(() => ({}));
  return new Error(body?.error || "HTTP " + res.status);
}

// getPush asks the gateway whether it can push and whether it knows this endpoint. An older
// gateway answers 404, which reads as "unavailable".
export async function getPush(api: Api, endpoint = ""): Promise<PushView> {
  const res = await api(
    PUSH_PATH + (endpoint ? "?endpoint=" + encodeURIComponent(endpoint) : ""),
  );
  if (!res.ok) {
    return { available: false, subscribed: false };
  }
  return res.json();
}

// saveSubscription hands the browser's subscription to the gateway, with the language its
// notifications are to be written in.
export async function saveSubscription(
  api: Api,
  sub: PushSubscriptionJSON,
  lang: string,
): Promise<void> {
  const res = await api(PUSH_SUBSCRIPTIONS_PATH, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ ...sub, lang }),
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

export async function removeSubscription(
  api: Api,
  endpoint: string,
): Promise<void> {
  const res = await api(PUSH_SUBSCRIPTIONS_PATH, {
    method: "DELETE",
    headers: JSON_HEADERS,
    body: JSON.stringify({ endpoint }),
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

// testPush asks the gateway to send one notification to this browser, through the push service.
export async function testPush(api: Api, endpoint: string): Promise<void> {
  const res = await api(PUSH_TEST_PATH, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ endpoint }),
  });
  if (!res.ok) {
    throw await failure(res);
  }
}
