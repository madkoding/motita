import { useEffect } from "preact/hooks";
import type { Api } from "./prApi";
import { bindPrefs, syncPrefs } from "./serverPrefs";
import { SPEECH_PREFS_EVENT } from "./speech";
import { applyTheme, loadTheme } from "./theme";
import { USAGE_STYLE_EVENT, applySettings, loadSettings } from "./uiPrefs";
import { VOICE_PREFS_EVENT } from "./voice";

// usePrefsSync keeps the page's preferences on the gateway: once the browser holds a credential it
// reads them (and sends up the ones only this browser had), and from then on every save goes there.
// When the gateway's values differ from the ones this browser had, they are applied at once.
export function usePrefsSync(api: Api, authState: string) {
  useEffect(() => {
    if (authState !== "ok") {
      return undefined;
    }
    bindPrefs(api);
    let cancelled = false;
    (async () => {
      try {
        if ((await syncPrefs(api)) && !cancelled) {
          applyTheme(loadTheme());
          applySettings(loadSettings());
          for (const name of [
            SPEECH_PREFS_EVENT,
            VOICE_PREFS_EVENT,
            USAGE_STYLE_EVENT,
          ]) {
            window.dispatchEvent(new Event(name));
          }
        }
      } catch {
        /* an older gateway, or one out of reach: this browser's values stand */
      }
    })();
    return () => {
      cancelled = true;
      bindPrefs(null);
    };
  }, [authState]);
}
