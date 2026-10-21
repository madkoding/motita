// The page's preferences live on the gateway, so every browser the person opens shows the same
// ones. localStorage stays as this browser's copy (the first paint reads it before the gateway
// answers), and the gateway's value wins whenever the two differ.
//
// What is tied to a device is never sent: the microphone, the voice models downloaded into this
// browser, the density of a phone. SYNCED lists the keys that are kept, and for the ones that hold
// both kinds of value, the fields that stay here.
import type { Api } from "./gitApi";

export const PREFS_PATH = "/v1/ui/prefs";
export const PREFS_SYNCED_EVENT = "motita-prefs-synced";
export const PUSH_DELAY_MS = 300;

export const SYNCED: Record<string, readonly string[]> = {
  "motita.ui.settings": ["density"],
  "motita.ui.theme": [],
  "motita.ui.notifications": [],
  "motita.ui.ciToast": [],
  "motita.ui.nextHint": [],
  "motita.ui.usageStyle": [],
  "motita.ui.usageOpen": [],
  "motita.ui.speech": ["engine", "localVoice", "voiceURI", "langVoices"],
  "motita.ui.voice": ["mic", "listen", "deviceId", "engine", "listener"],
};

let current: Api | null = null;
const pending = new Set<string>();
let timer: ReturnType<typeof setTimeout> | undefined;

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function objectOf(raw: string | null): Record<string, unknown> | null {
  try {
    const v = JSON.parse(raw ?? "");
    if (v && typeof v === "object" && !Array.isArray(v)) {
      return v;
    }
  } catch {
    /* a plain value */
  }
  return null;
}

// shared is the value of a key without its device fields, as the gateway keeps it.
export function shared(key: string, raw: string): string {
  const own = SYNCED[key];
  const obj = objectOf(raw);
  if (!own.length || !obj) {
    return raw;
  }
  const kept = { ...obj };
  for (const f of own) {
    delete kept[f];
  }
  return JSON.stringify(kept);
}

// merged is the gateway's value laid over this browser's, with the device fields of the browser kept.
export function merged(
  key: string,
  server: string,
  local: string | null,
): string {
  const own = SYNCED[key];
  const theirs = objectOf(server);
  if (!own.length || !theirs) {
    return server;
  }
  const mine = objectOf(local) ?? {};
  const out = { ...theirs };
  for (const f of own) {
    if (f in mine) {
      out[f] = mine[f];
    }
  }
  return JSON.stringify(out);
}

// canonical writes an object's fields in a fixed order, so two spellings of one value compare equal.
function canonical(raw: string | null): string | null {
  const obj = objectOf(raw);
  if (!obj) {
    return raw;
  }
  return JSON.stringify(Object.fromEntries(Object.entries(obj).sort()));
}

async function put(api: Api, prefs: Record<string, string>): Promise<void> {
  await api(PREFS_PATH, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ prefs }),
  });
}

// bindPrefs says which gateway the saves go to; null (signed out) stops them.
export function bindPrefs(api: Api | null): void {
  current = api;
}

// pushPref sends the saved value of a key to the gateway, a moment later so a slider that saves
// on every step sends once. A failure is silent: the value stays here and the next save sends it.
export function pushPref(key: string): void {
  if (!current || !(key in SYNCED)) {
    return;
  }
  pending.add(key);
  clearTimeout(timer);
  timer = setTimeout(flushPrefs, PUSH_DELAY_MS);
}

export function flushPrefs(): void {
  clearTimeout(timer);
  const api = current;
  const prefs: Record<string, string> = {};
  for (const key of pending) {
    const raw = read(key);
    if (raw !== null) {
      prefs[key] = shared(key, raw);
    }
  }
  pending.clear();
  if (api && Object.keys(prefs).length) {
    put(api, prefs).catch(() => undefined);
  }
}

// syncPrefs brings this browser in line with the gateway. A key the gateway has replaces this
// browser's (its device fields aside); a key only this browser has is sent up once, so the choices
// made before the gateway kept them are not lost and never overwrite what it holds. It resolves to
// whether this browser's values changed.
export async function syncPrefs(api: Api): Promise<boolean> {
  if (pending.size) {
    return false; // a save still on its way would be overwritten by an older value
  }
  const res = await api(PREFS_PATH);
  if (!res.ok) {
    return false;
  }
  const server: Record<string, string> = (await res.json()).prefs ?? {};
  const up: Record<string, string> = {};
  let changed = false;
  for (const key of Object.keys(SYNCED)) {
    const local = read(key);
    const theirs = server[key];
    if (typeof theirs === "string") {
      const next = merged(key, theirs, local);
      if (canonical(next) !== canonical(local)) {
        try {
          localStorage.setItem(key, next);
          changed = true;
        } catch {
          /* storage is full or blocked: this browser keeps what it had */
        }
      }
    } else if (local !== null) {
      up[key] = shared(key, local);
    }
  }
  if (Object.keys(up).length) {
    await put(api, up);
  }
  return changed;
}
