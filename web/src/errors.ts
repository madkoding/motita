import { t } from "./i18n";
import { NO_WEBGPU } from "./localVoiceCore";

// errorMessage is the text a failure is shown with: an Error's message, anything else as text.
export function errorMessage(e: unknown): string {
  if (e instanceof Error) {
    return e.message;
  }
  return String(e);
}

// workerError is a failure that came from a worker, in the reader's language. The workers speak
// English (they have no catalog of words), so the few messages a person can meet are named here;
// anything else is shown as it came.
export function workerError(message: string): string {
  switch (message) {
    case NO_WEBGPU:
      return t("This browser has no WebGPU for the page's workers.");
    case "the browser gave no graphics card to use":
      return t("The browser gave no graphics card to use.");
    case "the local listener stopped":
      return t("The local listener stopped.");
    default:
      return message;
  }
}
