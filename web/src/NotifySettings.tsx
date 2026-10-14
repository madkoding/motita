import { useCallback, useState } from "preact/hooks";
import { InsecureWarning } from "./InsecureWarning";
import { t } from "./i18n";
import {
  loadNotify,
  saveNotify,
  loadCiToast,
  saveCiToast,
  notifyState,
  type NotifyState,
  type NotifyPlatform,
} from "./settings";
import { choose } from "./choose";
import { showNotification } from "./notify";
import type { Api } from "./gitApi";
import {
  NOTIFY_GUIDE,
  currentPlatform,
  isInstalled,
  isSecurePage,
  readPermission,
  requestNotify,
  usePermissionRedraw,
} from "./notifyBrowser";
import { PushSetting } from "./PushSetting";
import { Row, smallBtn } from "./SettingsControls";

// notifyHint explains the desktop-notification switch for the state the browser is in.
// An insecure page has no hint: it shows the InsecureWarning pill instead.
function notifyHint(
  ns: NotifyState,
  platform: NotifyPlatform,
): string | undefined {
  if (ns === "insecure") {
    return undefined;
  }
  if (ns === "unsupported") {
    if (platform === "ios") {
      return t(
        "On iPhone and iPad they only work in the installed app: tap Share, then Add to Home Screen, and open Motita from there.",
      );
    }
    return t("This browser cannot show notifications.");
  }
  if (ns === "blocked") {
    return t("Blocked in this browser. To turn them back on:");
  }
  if (ns === "ask") {
    return t(
      "Only while this tab is in the background. The browser has to allow it first.",
    );
  }
  return t("Only while this tab is in the background.");
}

// unblockSteps says where this browser (or the installed app) keeps the permission once it was
// refused: the page itself can no longer ask, only the person can allow it again.
export function unblockSteps(
  platform: NotifyPlatform,
  installed: boolean,
): string[] {
  const reload = t("Come back here: the switch shows up on its own.");
  if (platform === "ios") {
    return [
      t("Open the Settings app, then Notifications."),
      t("Find Motita in the list and turn on Allow Notifications."),
      reload,
    ];
  }
  if (platform === "android") {
    if (installed) {
      return [
        t("Touch and hold the Motita icon, then tap App info."),
        t("Open Notifications and turn them on."),
        reload,
      ];
    }
    return [
      t("Tap the icon to the left of the address, then Permissions."),
      t("Turn Notifications on."),
      reload,
    ];
  }
  if (platform === "firefox") {
    return [
      t("Click the icon to the left of the address."),
      t("Next to Send notifications, click × to clear the block."),
      t("Reload the page and press Allow here."),
    ];
  }
  if (platform === "safari") {
    return [
      t("Open Safari, then Settings, then Websites, then Notifications."),
      t("Set this site to Allow."),
      reload,
    ];
  }
  if (platform === "chrome" || platform === "edge") {
    return [
      choose(
        installed,
        t("Open the app's menu (⋮ or ⋯ at the top), then App info."),
        t("Click the icon to the left of the address."),
      ),
      t("Set Notifications to Allow (or press Reset permission)."),
      t("Reload the page."),
    ];
  }
  return [
    t(
      "Open this site's settings in the browser (usually the icon to the left of the address).",
    ),
    t("Allow notifications."),
    t("Reload the page."),
  ];
}

// CiToastSetting is the toast inside the page when a CI ends: on by default, and usable on any
// address, since it needs no permission from the browser.
function CiToastSetting() {
  const [on, setOn] = useState(() => loadCiToast());
  return (
    <div class="settings-card" data-testid="ci-toast-setting">
      <Row
        id="settings-ci-toast"
        label={t("Notice when a CI ends")}
        hint={t("A message inside Motita. Works on any connection.")}
      >
        <label class="settings-switch-hit">
          <input
            type="checkbox"
            role="switch"
            class="settings-switch"
            aria-labelledby="settings-ci-toast"
            checked={on}
            onChange={(e) => {
              const v = (e.target as HTMLInputElement).checked;
              setOn(v);
              saveCiToast(v);
            }}
          />
        </label>
      </Row>
    </div>
  );
}

// NotifyControl is the control of the notifications row for the state the browser is in: a
// button that asks, one that reads a refused permission again, or the user's own switch.
function NotifyControl({
  ns,
  onAsk,
  onRecheck,
  onSwitch,
}: {
  ns: NotifyState;
  onAsk: () => void;
  onRecheck: () => void;
  onSwitch: (on: boolean) => void;
}) {
  if (ns === "ask") {
    return (
      <button type="button" class={smallBtn} onClick={onAsk}>
        {t("Allow notifications")}
      </button>
    );
  }
  if (ns === "blocked") {
    return (
      <button type="button" class={smallBtn} onClick={onRecheck}>
        {t("Check again")}
      </button>
    );
  }
  if (ns !== "on" && ns !== "off") {
    return null;
  }
  return (
    <label class="settings-switch-hit">
      <input
        type="checkbox"
        role="switch"
        class="settings-switch"
        aria-labelledby="settings-notify"
        checked={ns === "on"}
        onChange={(e) => onSwitch((e.target as HTMLInputElement).checked)}
      />
    </label>
  );
}

// NotifyTestRow shows one notification on demand, to check that they arrive. The outcome lives in
// NotifySetting, so it outlasts the switch going off and on.
function NotifyTestRow({
  tested,
  setTested,
}: {
  tested: string | undefined;
  setTested: (outcome: string) => void;
}) {
  const send = () => {
    void showNotification(t("Motita notifications work"), {
      body: t("This is how a CI that ends will look."),
      tag: "motita-test",
    }).then((ok) =>
      setTested(
        choose(
          ok,
          t(
            "Sent. If nothing showed up, check the system's do-not-disturb mode.",
          ),
          t("The browser did not show it."),
        ),
      ),
    );
  };
  return (
    <Row
      id="settings-notify-test"
      label={t("Test notification")}
      hint={tested ?? t("Shows one now, to check that they arrive.")}
    >
      <button type="button" class={smallBtn} onClick={send}>
        {t("Send")}
      </button>
    </Row>
  );
}

// NotifySetting is the desktop notifications for a CI that ends: a button that asks the browser while it has not been asked,
// the steps to allow them again once refused, and the user's own on/off switch once allowed.
export function NotifySetting({ push }: { push?: { api: Api } }) {
  const [notifyOn, setNotifyOn] = useState(() => loadNotify());
  // The browser's answer is read on every render (see usePermissionRedraw).
  const redraw = usePermissionRedraw();
  const [asked, setAsked] = useState(false);
  const [tested, setTested] = useState<string | undefined>();
  const permission = readPermission();
  const ns = notifyState(permission, notifyOn, isSecurePage());
  const platform = currentPlatform();
  // A browser that quietly ignored the request (Chrome after a few dismissals) leaves it at
  // "default": the steps are the only way left then too.
  const ignored = ns === "ask" && asked;
  const showSteps = ns === "blocked" || ignored;
  // Handed to NotifyControl, so they keep their identity between renders.
  const turn = useCallback((on: boolean) => {
    setNotifyOn(on);
    saveNotify(on);
  }, []);
  const ask = useCallback(() => {
    requestNotify()
      .then((r) => {
        if (r === "granted") {
          turn(true);
        }
        // No promise back: the answer comes through the focus listener alone.
        return r !== undefined;
      })
      .catch(() => true)
      .then((answered) => {
        if (answered) {
          setAsked(true);
          redraw();
        }
      });
  }, [turn, redraw]);
  return (
    <section class="settings-group" aria-labelledby="settings-notify-h">
      <h3 id="settings-notify-h" class="settings-group-title">
        {t("Notifications")}
      </h3>
      <CiToastSetting />
      <div class="settings-card" data-testid="notifications-setting">
        <Row
          id="settings-notify"
          label={t("Desktop notification")}
          warning={
            ns === "insecure" && (
              <InsecureWarning
                text={t(
                  "Your browser blocks notifications because Motita was opened without a secure connection.",
                )}
                guide={NOTIFY_GUIDE}
              />
            )
          }
          hint={choose(
            ignored,
            t("The browser did not ask. To allow them by hand:"),
            notifyHint(ns, platform),
          )}
        >
          <NotifyControl
            ns={ns}
            onAsk={ask}
            onRecheck={redraw}
            onSwitch={turn}
          />
        </Row>
        {ns === "on" && <NotifyTestRow tested={tested} setTested={setTested} />}
        {showSteps && (
          <ol class="settings-steps" data-testid="notify-steps">
            {unblockSteps(platform, isInstalled()).map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
        )}
      </div>
      {push && <PushSetting api={push.api} />}
    </section>
  );
}
