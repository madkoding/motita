// Client for the gateway's boxes API: the service containers Motita started so it can test a
// project and photograph it. No value imports: the node tests load this file as is.

import type { Api } from "./gitApi";

export interface BoxInfo {
  id: string;
  name: string;
  image: string;
  status: string;
  created: string;
}

export interface BoxesView {
  // The container tool found on the machine, "" when there is none.
  runtime: string;
  boxes: BoxInfo[];
}

const HTTP_NOT_FOUND = 404;
export const BOXES_PATH = "/v1/boxes";

async function failure(res: Response): Promise<Error> {
  const body = await res.json().catch(() => ({}));
  return new Error(body?.error || "HTTP " + res.status);
}

// getBoxes reads which boxes exist, or null from a gateway that has no boxes API (404).
export async function getBoxes(api: Api): Promise<BoxesView | null> {
  const res = await api(BOXES_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  if (!res.ok) {
    throw await failure(res);
  }
  return (await res.json()) as BoxesView;
}

// removeBox stops and removes one box.
export async function removeBox(api: Api, id: string): Promise<void> {
  const res = await api(BOXES_PATH + "/" + encodeURIComponent(id), {
    method: "DELETE",
  });
  if (!res.ok) {
    throw await failure(res);
  }
}

// stopBoxes removes every box and says how many there were.
export async function stopBoxes(api: Api): Promise<number> {
  const res = await api(BOXES_PATH, { method: "DELETE" });
  if (!res.ok) {
    throw await failure(res);
  }
  return ((await res.json()) as { removed: number }).removed;
}
