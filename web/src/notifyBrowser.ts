import { useCallback, useEffect, useState } from "preact/hooks";
import { notifyPlatform, type NotifyPlatform } from "./uiPrefs";

// The guide section that says how to give the page a secure address.
export const NOTIFY_GUIDE =
  "https://github.com/madkoding/motita/blob/main/docs/GUIDE.md#the-browser-interface-from-the-same-port";

// isInstalled says whether the page runs as the installed app rather than in a browser tab.
export function isInstalled(): boolean {
  try {
    return (
      window.matchMedia?.("(display-mode: standalone)").matches ||
      (navigator as Navigator & { standalone?: boolean }).standalone === true
    );
  } catch {
    return false;
  }
}

// readPermission is the browser's answer right now, or undefined where there are no notifications.
export function readPermission(): NotificationPermission | undefined {
  if (typeof Notification === "undefined") {
    return undefined;
  }
  return Notification.permission;
}

// isSecurePage says whether the page was opened over a secure connection (https or localhost),
// which notifications and push need. Outside a browser it counts as secure.
export function isSecurePage(): boolean {
  return typeof window === "undefined" || window.isSecureContext !== false;
}

// currentPlatform is the browser or system the steps and hints are written for.
export function currentPlatform(): NotifyPlatform {
  return notifyPlatform(navigator.userAgent, navigator.maxTouchPoints || 0);
}

// usePermissionRedraw redraws the component whenever the browser's notification permission may
// have changed: it can change in the browser's own settings while this is open, so a permission
// change and coming back to the tab both draw it again. It returns the function that redraws.
export function usePermissionRedraw(): () => void {
  const [, bump] = useState(0);
  const again = useCallback(() => bump((n) => n + 1), []);
  useEffect(() => {
    window.addEventListener("focus", again);
    document.addEventListener("visibilitychange", again);
    let status: PermissionStatus | undefined;
    navigator.permissions
      ?.query({ name: "notifications" as PermissionName })
      .then((s) => {
        status = s;
        s.addEventListener("change", again);
      })
      .catch(() => {
        /* no Permissions API: focus is enough */
      });
    return () => {
      window.removeEventListener("focus", again);
      document.removeEventListener("visibilitychange", again);
      status?.removeEventListener("change", again);
    };
  }, [again]);
  return again;
}

// requestNotify asks the browser for the permission. It resolves to undefined where the browser
// gives no promise back: Safari before 15 only takes a callback, and the focus listener redraws.
// A call that throws becomes a rejected promise.
export function requestNotify(): Promise<NotificationPermission | undefined> {
  return Promise.resolve().then(
    () =>
      Notification.requestPermission() as
        | Promise<NotificationPermission>
        | undefined,
  );
}
