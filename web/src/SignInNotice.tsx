// SignInNotice is a turn that failed because a provider's sign-in stopped working: claude's own
// login, or a stored Copilot, Codex, Qwen or Google one. The gateway's words stay as they are, and
// a button opens the providers' settings, where the provider shows as expired and signs in again.

import { t } from "./i18n";

// SIGN_IN_FAILURE matches the gateway's messages for a sign-in that cannot be renewed (see
// internal/llm/claudesignin.go and auth.go): both name the way back in the settings.
const SIGN_IN_FAILURE = /sign in again in Settings → Providers/;

// isSignInFailure says whether an error line is a sign-in that stopped working.
export function isSignInFailure(text: string): boolean {
  return SIGN_IN_FAILURE.test(text);
}

export function SignInNotice({
  text,
  onReconnect,
}: {
  text: string;
  onReconnect: () => void;
}) {
  return (
    <span class="signin-notice">
      <span>{text}</span>{" "}
      <button
        type="button"
        class="text-xs text-accent underline underline-offset-2"
        onClick={onReconnect}
      >
        {t("Reconnect the provider")}
      </button>
    </span>
  );
}
