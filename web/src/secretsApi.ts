// Client for the gateway's secrets API (/v1/secrets): the variables the person keeps for the
// agent's commands. A value is sent and never comes back; a name carries at most the last
// characters of a long value. No value imports: the node tests load this file as is.

import type { Api } from "./gitApi";

export interface SecretItem {
  name: string;
  // The last four characters of a value of 16 or more; absent for a shorter one.
  hint?: string;
}

export interface SecretsView {
  can_save: boolean;
  secrets: SecretItem[];
  max: number;
}

const HTTP_NOT_FOUND = 404;
export const SECRETS_PATH = "/v1/secrets";

async function view(res: Response): Promise<SecretsView> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error || "HTTP " + res.status);
  }
  return (await res.json()) as SecretsView;
}

// getSecrets reads the variables, or null from a gateway that has no secrets API (404).
export async function getSecrets(api: Api): Promise<SecretsView | null> {
  const res = await api(SECRETS_PATH);
  if (res.status === HTTP_NOT_FOUND) {
    return null;
  }
  return view(res);
}

// saveSecret stores a variable, replacing one of the same name.
export async function saveSecret(
  api: Api,
  name: string,
  value: string,
): Promise<SecretsView> {
  return view(
    await api(SECRETS_PATH + "/" + encodeURIComponent(name), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ value }),
    }),
  );
}

// deleteSecret removes a variable.
export async function deleteSecret(
  api: Api,
  name: string,
): Promise<SecretsView> {
  return view(
    await api(SECRETS_PATH + "/" + encodeURIComponent(name), {
      method: "DELETE",
    }),
  );
}

// NAME_PATTERN is what a variable may be called, as the gateway checks it.
export const NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
