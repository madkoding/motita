// Client for the gateway's friction journal: what got in the way, kept on the computer the gateway
// runs on. The settings read the week back as plain words; "this was not right" on an answer
// writes one entry. No value imports: the node tests load this file as is.

import type { Api } from "./gitApi";

export const FRICTION_PATH = "/v1/friction";
const JSON_HEADERS = { "Content-Type": "application/json" };
const HTTP_NOT_FOUND = 404;

export interface FrictionRepeat {
  kind: string;
  text: string;
  count: number;
}

export interface FrictionFlag {
  at: string;
  ask?: string;
  note?: string;
}

export interface FrictionView {
  days: number;
  total: number;
  counts: Record<string, number>;
  repeated: FrictionRepeat[];
  flagged: FrictionFlag[];
  // The summary in the interface's own words, written by the gateway.
  text: string;
}

async function failure(res: Response): Promise<Error> {
  const body = await res.json().catch(() => ({}));
  return new Error(body?.error || "HTTP " + res.status);
}

// getFriction reads the week, in the page's language, or null from a gateway that has no journal
// (404).
export async function getFriction(
  api: Api,
  lang: string,
): Promise<FrictionView | null> {
  const res = await api(FRICTION_PATH + "?lang=" + encodeURIComponent(lang));
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  if (!res.ok) {
    throw await failure(res);
  }
  return (await res.json()) as FrictionView;
}

// clearFriction forgets the whole journal.
export async function clearFriction(api: Api): Promise<void> {
  const res = await api(FRICTION_PATH, { method: "DELETE" });
  if (!res.ok) {
    throw await failure(res);
  }
}

// flagAnswer says the answer that ended a turn was not right, with the person's own words when
// they wrote any. The gateway adds what it knows of the turn.
export async function flagAnswer(
  api: Api,
  sessionId: string,
  turn: number,
  note: string,
): Promise<void> {
  const res = await api(
    "/v1/sessions/" + encodeURIComponent(sessionId) + "/friction",
    {
      method: "POST",
      headers: JSON_HEADERS,
      body: JSON.stringify({ turn, note }),
    },
  );
  if (!res.ok) {
    throw await failure(res);
  }
}
