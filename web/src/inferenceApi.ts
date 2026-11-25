// Client for the gateway's inference-servers API (/v1/inference-servers): the list of servers that
// serve models over HTTP. A key is sent and never comes back; a server says whether it has one.
// No value imports: the node tests load this file as is.

import type { Api } from "./gitApi";

export interface InferenceKind {
  id: string;
  name: string;
  default_base_url: string;
  provider: string;
}

export interface InferenceServer {
  id: string;
  name: string;
  kind: string;
  base_url: string;
  model?: string;
  provider: string;
  has_key: boolean;
  key_hint?: string;
  // The provider runs with this server's address now.
  in_use: boolean;
  // Motita's own local model: listed, but managed in Settings → Providers.
  managed?: boolean;
}

export interface InferenceView {
  can_save: boolean;
  max: number;
  kinds: InferenceKind[];
  servers: InferenceServer[];
}

export interface ProbeResult {
  ok: boolean;
  latency_ms: number;
  models?: string[];
  error?: string;
}

// InferenceBody is what an add or an edit sends; a field left out stays as it was.
export interface InferenceBody {
  name?: string;
  kind?: string;
  base_url?: string;
  model?: string;
  api_key?: string;
}

const HTTP_NOT_FOUND = 404;
export const INFERENCE_PATH = "/v1/inference-servers";

async function read<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as T;
}

function send(method: string, body?: InferenceBody) {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  };
}

// getInference reads the list, or null from a gateway that has no such API (404).
export async function getInference(api: Api): Promise<InferenceView | null> {
  const res = await api(INFERENCE_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  return read<InferenceView>(res);
}

export async function addInference(
  api: Api,
  body: InferenceBody,
): Promise<InferenceView> {
  return read(await api(INFERENCE_PATH, send("POST", body)));
}

export async function editInference(
  api: Api,
  id: string,
  body: InferenceBody,
): Promise<InferenceView> {
  return read(
    await api(INFERENCE_PATH + "/" + encodeURIComponent(id), send("PUT", body)),
  );
}

export async function removeInference(
  api: Api,
  id: string,
): Promise<InferenceView> {
  return read(
    await api(INFERENCE_PATH + "/" + encodeURIComponent(id), {
      method: "DELETE",
    }),
  );
}

// probeInference asks the server for its models; the answer says how it went.
export async function probeInference(
  api: Api,
  id: string,
): Promise<ProbeResult> {
  return read(
    await api(
      INFERENCE_PATH + "/" + encodeURIComponent(id) + "/probe",
      send("POST"),
    ),
  );
}

// selectInference makes a server the endpoint of the provider that speaks its kind.
export async function selectInference(
  api: Api,
  id: string,
): Promise<InferenceView> {
  return read(
    await api(
      INFERENCE_PATH + "/" + encodeURIComponent(id) + "/use",
      send("POST"),
    ),
  );
}
