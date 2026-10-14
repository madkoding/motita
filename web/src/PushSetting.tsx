import { useCallback, useEffect, useState } from "preact/hooks";
import { InsecureWarning } from "./InsecureWarning";
import { lang, t } from "./i18n";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import {
  type PushState,
  type PushView,
  getPush,
  keyBytes,
  loadPushOn,
  pushState,
  pushSupported,
  removeSubscription,
  savePushOn,
  saveSubscription,
  testPush,
} from "./pushApi";
import {
  NOTIFY_GUIDE,
  currentPlatform,
  isInstalled,
  isSecurePage,
  readPermission,
  requestNotify,
  usePermissionRedraw,
} from "./notifyBrowser";
import { Row, smallBtn } from "./SettingsControls";

// pushHint explains the phone-notification switch for the state the device and the gateway are in.
function pushHint(ps: PushState | undefined): string {
  switch (ps) {
    case "install":
      return t(
        "On iPhone and iPad they only work in the installed app: tap Share, then Add to Home Screen, and open Motita from there.",
      );
    case "unsupported":
      return t("This browser cannot receive push notifications.");
    case "unavailable":
      return t(
        "This gateway cannot send them: it has no folder to keep them in, or it is older than this page.",
      );
    case "blocked":
      return t(
        "Notifications are blocked for this site: allow them first (see the steps above).",
      );
    default:
      return t(
        "Even with Motita closed: the gateway sends them to this device when a CI ends.",
      );
  }
}

// usePushState reads, and keeps current, what the device and the gateway say about push.
function usePushState(api: Api) {
  const [gateway, setGateway] = useState<PushView | null>(null);
  const [sub, setSub] = useState<PushSubscription | null>(null);
  const [read, setRead] = useState(false);
  const supported = pushSupported();
  const refresh = useCallback(async () => {
    let current: PushSubscription | null = null;
    if (supported) {
      try {
        const reg = await navigator.serviceWorker.getRegistration();
        current = (await reg?.pushManager.getSubscription()) ?? null;
      } catch {
        current = null;
      }
    }
    let view: PushView = { available: false, subscribed: false };
    try {
      view = await getPush(api, current?.endpoint ?? "");
    } catch {
      /* the gateway did not answer: shown as unavailable */
    }
    // The browser holds a subscription the gateway forgot (its folder was cleared): hand it over
    // again, so the switch the person turned on stays on.
    if (current && view.available && !view.subscribed && loadPushOn()) {
      try {
        await saveSubscription(api, current.toJSON(), lang());
        view = { ...view, subscribed: true };
      } catch {
        /* shown as off */
      }
    }
    setSub(current);
    setGateway(view);
    setRead(true);
  }, [api, supported]);
  useEffect(() => {
    void refresh();
  }, [refresh]);
  return { gateway, sub, read, supported, refresh };
}

// pushHintFor is the hint of the phone-notification row: none on an insecure page, where the pill
// says it.
function pushHintFor(ps: PushState | undefined): string | undefined {
  if (ps === "insecure") {
    return undefined;
  }
  return pushHint(ps);
}

// pushWarning is what goes under the phone-notification row: the pill on an insecure page, else
// the last error, if any.
function pushWarning(ps: PushState | undefined, error: string) {
  if (ps === "insecure") {
    return (
      <InsecureWarning
        text={t(
          "Your browser blocks notifications because Motita was opened without a secure connection.",
        )}
        guide={NOTIFY_GUIDE}
      />
    );
  }
  if (!error) {
    return undefined;
  }
  return (
    <p class="text-xs text-danger mt-1" role="alert">
      {error}
    </p>
  );
}

// currentPushState is the state of the switch once the device and the gateway have been read.
function currentPushState(
  read: boolean,
  supported: boolean,
  gateway: PushView | null,
  sub: PushSubscription | null,
): PushState | undefined {
  if (!read) {
    return undefined;
  }
  return pushState({
    secure: isSecurePage(),
    supported,
    ios: currentPlatform() === "ios",
    installed: isInstalled(),
    gateway,
    permission: readPermission(),
    subscribed: !!sub,
  });
}

// subscribeHere turns push on for this device: the browser's permission, a subscription (the one
// it already holds, or a new one for the gateway's key), handed to the gateway.
async function subscribeHere(
  api: Api,
  gateway: PushView | null,
): Promise<string> {
  const permission = await requestNotify().catch(() => undefined);
  if (permission !== "granted" && readPermission() !== "granted") {
    return t("The browser did not allow notifications.");
  }
  const reg = await navigator.serviceWorker.ready;
  const next =
    (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: keyBytes(gateway?.public_key ?? ""),
    }));
  await saveSubscription(api, next.toJSON(), lang());
  savePushOn(true);
  return "";
}

// unsubscribeHere turns push off for this device: the gateway forgets the subscription and the
// browser drops it.
async function unsubscribeHere(
  api: Api,
  sub: PushSubscription | null,
): Promise<string> {
  if (sub) {
    await removeSubscription(api, sub.endpoint).catch(() => undefined);
    await sub.unsubscribe().catch(() => false);
  }
  savePushOn(false);
  return "";
}

// PushSetting is the phone notifications: the gateway pushes a CI that ended to this device, even
// with the page closed. Turning it on asks the browser's permission, subscribes, and hands the
// subscription to the gateway; off undoes both.
export function PushSetting({ api }: { api: Api }) {
  const { gateway, sub, read, supported, refresh } = usePushState(api);
  usePermissionRedraw();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [tested, setTested] = useState<string | undefined>();
  const ps = currentPushState(read, supported, gateway, sub);
  const toggle = (on: boolean) => {
    setBusy(true);
    setError("");
    setTested(undefined);
    let change: Promise<string>;
    if (on) {
      change = subscribeHere(api, gateway);
    } else {
      change = unsubscribeHere(api, sub);
    }
    change
      .then((refused) => {
        if (refused) {
          setError(refused);
        }
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => {
        setBusy(false);
        void refresh();
      });
  };
  const sendTest = () => {
    if (!sub) {
      return;
    }
    setTested(t("Sending…"));
    testPush(api, sub.endpoint)
      .then(() =>
        setTested(
          t(
            "Sent. If nothing showed up, check the system's do-not-disturb mode.",
          ),
        ),
      )
      .catch((e) => setTested(errorMessage(e)));
  };
  const usable = ps === "on" || ps === "off";
  return (
    <div class="settings-card" data-testid="push-setting">
      <Row
        id="settings-push"
        label={t("Phone notifications")}
        hint={pushHintFor(ps)}
        warning={pushWarning(ps, error)}
      >
        {usable && (
          <label class="settings-switch-hit">
            <input
              type="checkbox"
              role="switch"
              class="settings-switch"
              aria-labelledby="settings-push"
              checked={ps === "on"}
              disabled={busy}
              onChange={(e) => toggle((e.target as HTMLInputElement).checked)}
            />
          </label>
        )}
      </Row>
      {ps === "on" && (
        <Row
          id="settings-push-test"
          label={t("Test on this device")}
          hint={
            tested ??
            t("The gateway sends one now, the same way a CI that ends will.")
          }
        >
          <button type="button" class={smallBtn} onClick={sendTest}>
            {t("Send")}
          </button>
        </Row>
      )}
    </div>
  );
}
