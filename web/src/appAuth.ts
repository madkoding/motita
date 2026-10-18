// The browser's credential: the cookie the gateway sets when a token is traded for it.
import { choose } from "./choose";
import {
  HTTP_FORBIDDEN,
  HTTP_UNAUTHORIZED,
  SAME_ORIGIN,
  SESSIONS_URL,
  STORAGE_KEY,
  WEBUI_SESSION_URL,
} from "./appConst";

// GateResult is what asking the gateway about a credential can come to: it accepted it, it
// answered and refused it, or it did not answer at all (no connection, or a proxy in front of a
// gateway that is down). Only "rejected" is a reason to ask for the token.
export type GateResult = "ok" | "rejected" | "offline";

// HTTP statuses that say "the server answered and does not accept this credential".
const REFUSED = new Set([HTTP_UNAUTHORIZED, HTTP_FORBIDDEN]);

function gateOf(res: Response): GateResult {
  if (res.ok) {
    return "ok";
  }
  return choose(REFUSED.has(res.status), "rejected", "offline");
}

// authGate checks whether the browser currently holds a valid gateway
// credential. It hits a PROTECTED endpoint: /v1/health is public, so it would
// always return true. /v1/sessions requires the cookie and returns 401 without
// one, which is the actual signal we need.
export async function authGate(): Promise<GateResult> {
  try {
    return gateOf(await fetch(SESSIONS_URL, { credentials: SAME_ORIGIN }));
  } catch {
    return "offline";
  }
}

// clearCredential asks the gateway to delete the cookie and also clears any
// local storage the app keeps, so nothing survives a rejected token.
export async function clearCredential(): Promise<void> {
  try {
    await fetch(WEBUI_SESSION_URL, {
      method: "DELETE",
      credentials: SAME_ORIGIN,
      headers: { "X-Motita": "1" },
    });
  } catch {
    /* ignore */
  }
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* ignore */
  }
}

// submitToken trades a token (or password) the user typed for the browser's
// cookie. Says whether the gateway took it, refused it, or could not be reached.
export async function submitToken(token: string): Promise<GateResult> {
  try {
    const res = await fetch(WEBUI_SESSION_URL, {
      method: "POST",
      credentials: SAME_ORIGIN,
      headers: { Authorization: "Bearer " + token },
    });
    return gateOf(res);
  } catch {
    return "offline";
  }
}

// exchange trades the URL fragment for the cookie.
// The fragment is never sent to the server; once traded, it is erased.
export async function exchange(): Promise<boolean> {
  const hash = window.location.hash || "";
  const marker = hash.indexOf("t=");
  if (marker === -1) {
    return false;
  }
  const token = hash.slice(marker + 2);
  if (!token) {
    return false;
  }
  try {
    const res = await fetch(WEBUI_SESSION_URL, {
      method: "POST",
      credentials: SAME_ORIGIN,
      headers: { Authorization: "Bearer " + token },
    });
    // Erased whatever the answer: a token that did not work is not one to keep.
    history.replaceState(
      null,
      "",
      window.location.pathname + window.location.search,
    );
    return res.ok;
  } catch {
    history.replaceState(
      null,
      "",
      window.location.pathname + window.location.search,
    );
    return false;
  }
}
