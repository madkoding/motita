import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { ModalBackdrop } from "./ModalBackdrop";
import { useDialog } from "./useDialog";
import { IconClose } from "./icons";
import { InsecureWarning } from "./InsecureWarning";
import "./dialog.css";
import { t, tf } from "./i18n";
import {
  type Api,
  type GitAccount,
  listAccounts,
  disconnect,
  trimTrailingSlashes,
} from "./gitApi";
import type { LangSetting } from "./i18n";
import {
  initSettings,
  saveSettings,
  loadNotify,
  saveNotify,
  loadCiToast,
  saveCiToast,
  notifyState,
  notifyPlatform,
  type NotifyState,
  type NotifyPlatform,
  type UISettings,
  type FontSize,
  type Density,
} from "./settings";
import { choose } from "./choose";
import { showNotification } from "./notify";
import { errorMessage } from "./errors";
import type { VoicePrefs, VoiceState } from "./voice";
import {
  ProviderSettings,
  type ProviderSettingsProps,
} from "./ProviderSettings";
import {
  type ChosenMode,
  type NetworkMode,
  type NetworkView,
  getNetwork,
  setNetworkMode,
  setTailscale,
  restartGateway,
} from "./networkApi";
import { copyText } from "./clipboard";

initSettings();

export { InsecureWarning };

interface Props {
  langSetting: LangSetting;
  langAvailable: boolean;
  onLang: (s: LangSetting) => void;
  onClose: () => void;
  // The git host connections section. Absent on a gateway that has no git API.
  git?: {
    api: Api;
    // Bumped by the app after a host was connected, so the list is read again.
    rev: number;
    onConnect: (
      service?: string,
      selfHosted?: { kind: string; host: string },
    ) => void;
    onNotice: (message: string, error?: boolean) => void;
  };
  // Speech input. Absent where the page has no voice store (the tests of other sections).
  voice?: VoiceSettingsProps;
  // The LLM providers and the default model. Absent where the page has no API to reach them.
  llm?: ProviderSettingsProps;
  // How the gateway is exposed on the network. Absent where the page has no API to hand it, and
  // the section stays hidden on a gateway without the network API.
  network?: { api: Api };
}

export interface VoiceSettingsProps {
  state: VoiceState;
  hands: VoiceState | "no-phrases";
  phrases: string[];
  prefs: VoicePrefs;
  setPrefs: (p: VoicePrefs) => void;
}

// voiceBlocker explains why speech input cannot be used, or "" when it can. An insecure page is
// not one of them: that one is the InsecureWarning pill (see voiceWarning).
function voiceBlocker(state: string): string {
  if (state === "unsupported") {
    return t("This browser cannot record audio.");
  }
  if (state === "off") {
    return t("Off on the gateway: set voice.enabled in the configuration.");
  }
  if (state === "no-phrases") {
    return t("Add voice.wake_phrases to the configuration first.");
  }
  return "";
}

// The guide sections that say how to give the page a secure address.
const NOTIFY_GUIDE =
  "https://github.com/madkoding/motita/blob/main/docs/GUIDE.md#the-browser-interface-from-the-same-port";
const VOICE_GUIDE =
  "https://github.com/madkoding/motita/blob/main/docs/REFERENCE.md#voice-input";

// voiceWarning is the pill for a voice switch the browser hides on an insecure page.
function voiceWarning(state: string) {
  if (state !== "insecure") {
    return undefined;
  }
  return (
    <InsecureWarning
      text={t(
        "Your browser blocks the microphone because Motita was opened without a secure connection.",
      )}
      guide={VOICE_GUIDE}
    />
  );
}

// VoiceSwitch is one switch of the speech section, greyed out while it cannot be used.
function VoiceSwitch({
  id,
  usable,
  checked,
  onChange,
}: {
  id: string;
  usable: boolean;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label class="settings-switch-hit">
      <input
        type="checkbox"
        role="switch"
        class="settings-switch"
        aria-labelledby={id}
        disabled={!usable}
        checked={usable && checked}
        onChange={(e) => onChange((e.target as HTMLInputElement).checked)}
      />
    </label>
  );
}

// VoiceSettings are the microphone and the wake phrase, which only work where the browser grants
// the microphone (https or localhost) and the gateway has speech input on.
function VoiceSettings({
  state,
  hands,
  phrases,
  prefs,
  setPrefs,
}: VoiceSettingsProps) {
  const setMic = useCallback(
    (mic: boolean) => setPrefs({ ...prefs, mic }),
    [prefs, setPrefs],
  );
  const setListen = useCallback(
    (listen: boolean) => setPrefs({ ...prefs, listen }),
    [prefs, setPrefs],
  );
  return (
    <section class="settings-group" aria-labelledby="settings-voice-h">
      <h3 id="settings-voice-h" class="settings-group-title">
        {t("Voice")}
      </h3>
      <div class="settings-card" data-testid="voice-setting">
        <Row
          id="settings-mic"
          label={t("Microphone button")}
          warning={voiceWarning(state)}
          hint={
            state === "insecure"
              ? undefined
              : voiceBlocker(state) ||
                t(
                  "Dictate into the message box. You read and edit the text before sending it.",
                )
          }
        >
          <VoiceSwitch
            id="settings-mic"
            usable={state === "ready"}
            checked={prefs.mic}
            onChange={setMic}
          />
        </Row>
        <Row
          id="settings-listen"
          label={t("Listen for the wake phrase")}
          warning={voiceWarning(hands)}
          hint={
            hands === "insecure"
              ? undefined
              : voiceBlocker(hands) ||
                tf(
                  "Only while this tab is open. What you say is sent to the transcription service. Say: {phrases}",
                  { phrases: phrases.map((p) => `“${p}”`).join(", ") },
                )
          }
        >
          <VoiceSwitch
            id="settings-listen"
            usable={hands === "ready"}
            checked={prefs.listen}
            onChange={setListen}
          />
        </Row>
      </div>
    </section>
  );
}

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

// isInstalled says whether the page runs as the installed app rather than in a browser tab.
function isInstalled(): boolean {
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
function readPermission(): NotificationPermission | undefined {
  if (typeof Notification === "undefined") {
    return undefined;
  }
  return Notification.permission;
}

type Tab = "general" | "providers" | "git";

interface Choice<T extends string> {
  value: T;
  label: string;
}

// Segmented draws a short list of choices as one row of radio buttons: every option is in sight
// and a finger picks it in one tap, where a <select> takes two and hides the others.
function Segmented<T extends string>({
  name,
  labelledBy,
  value,
  options,
  onChange,
  disabled,
}: {
  name: string;
  labelledBy: string;
  value: T;
  options: Choice<T>[];
  onChange: (v: T) => void;
  disabled?: boolean;
}) {
  return (
    <div role="radiogroup" aria-labelledby={labelledBy} class="settings-seg">
      {options.map((o) => (
        <label key={o.value} class="settings-seg-opt">
          <input
            type="radio"
            class="sr-only"
            name={name}
            value={o.value}
            checked={value === o.value}
            disabled={disabled}
            onChange={() => onChange(o.value)}
          />
          {o.label}
        </label>
      ))}
    </div>
  );
}

// Row is one setting: its name (and a hint or a warning under it) on one side and its control on
// the other. On a narrow phone the control goes under the name, full width (see .settings-row in
// index.css).
function Row({
  id,
  label,
  hint,
  stack,
  warning,
  children,
}: {
  id: string;
  label: string;
  hint?: ComponentChildren;
  // stack keeps the control under the name at every width, for a control too wide to share it.
  stack?: boolean;
  warning?: ComponentChildren;
  children: ComponentChildren;
}) {
  return (
    <div class={"settings-row" + choose(stack, " settings-row-stack", "")}>
      <div class="settings-row-text">
        <div id={id} class="text-sm text-[#e8e8ea]">
          {label}
        </div>
        {hint && <div class="text-xs text-[#7a7a8c] mt-0.5">{hint}</div>}
        {warning}
      </div>
      <div class="settings-row-control">{children}</div>
    </div>
  );
}

// GeneralSettings is how the page looks (language, font size, density) and the CI notifications.
function GeneralSettings({
  langSetting,
  langAvailable,
  onLang,
  voice,
  network,
}: Pick<
  Props,
  "langSetting" | "langAvailable" | "onLang" | "voice" | "network"
>) {
  const [s, setS] = useState<UISettings>(() => initSettings());
  const update = (next: UISettings) => {
    setS(next);
    saveSettings(next);
  };
  const setFontSize = useCallback(
    (fontSize: FontSize) => update({ ...s, fontSize }),
    [s],
  );
  const setDensity = useCallback(
    (density: Density) => update({ ...s, density }),
    [s],
  );
  return (
    <>
      <section class="settings-group" aria-labelledby="settings-look-h">
        <h3 id="settings-look-h" class="settings-group-title">
          {t("Appearance")}
        </h3>
        <div class="settings-card">
          {langAvailable && (
            <Row id="settings-lang" label={t("Language")}>
              <Segmented<LangSetting>
                name="settings-lang"
                labelledBy="settings-lang"
                value={langSetting}
                onChange={onLang}
                options={[
                  { value: "auto", label: t("Automatic") },
                  { value: "en", label: "English" },
                  { value: "es", label: "Español" },
                ]}
              />
            </Row>
          )}
          <Row id="settings-font" label={t("Font size")}>
            <Segmented<FontSize>
              name="settings-font"
              labelledBy="settings-font"
              value={s.fontSize}
              onChange={setFontSize}
              options={[
                { value: "small", label: t("Small") },
                { value: "normal", label: t("Normal") },
                { value: "large", label: t("Large") },
              ]}
            />
          </Row>
          <Row id="settings-density" label={t("Density")}>
            <Segmented<Density>
              name="settings-density"
              labelledBy="settings-density"
              value={s.density}
              onChange={setDensity}
              options={[
                { value: "comfortable", label: t("Comfortable") },
                { value: "compact", label: t("Compact") },
              ]}
            />
          </Row>
        </div>
      </section>
      <NotifySetting />
      {voice && <VoiceSettings {...voice} />}
      {network && <NetworkSettings api={network.api} />}
    </>
  );
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

// usePermissionRedraw redraws the component whenever the browser's notification permission may
// have changed: it can change in the browser's own settings while this is open, so a permission
// change and coming back to the tab both draw it again. It returns the function that redraws.
function usePermissionRedraw(): () => void {
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
function requestNotify(): Promise<NotificationPermission | undefined> {
  return Promise.resolve().then(
    () =>
      Notification.requestPermission() as
        | Promise<NotificationPermission>
        | undefined,
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
function NotifySetting() {
  const [notifyOn, setNotifyOn] = useState(() => loadNotify());
  // The browser's answer is read on every render (see usePermissionRedraw).
  const redraw = usePermissionRedraw();
  const [asked, setAsked] = useState(false);
  const [tested, setTested] = useState<string | undefined>();
  const permission = readPermission();
  const secure =
    typeof window === "undefined" || window.isSecureContext !== false;
  const ns = notifyState(permission, notifyOn, secure);
  const platform = notifyPlatform(
    navigator.userAgent,
    navigator.maxTouchPoints || 0,
  );
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
    </section>
  );
}

// The page Tailscale is installed from, opened in a new tab.
const TAILSCALE_DOWNLOAD = "https://tailscale.com/download";

// How the page waits for the gateway after asking it to restart: the first health check comes
// after firstPollMs (the process restarts about half a second after it answers), then one every
// pollMs, for up to waitMs. health and reload are here so the tests can stand in for them.
export const restartTiming = {
  firstPollMs: 1500,
  pollMs: 1000,
  waitMs: 30000,
  health: (): Promise<Response> =>
    fetch("/v1/health", { credentials: "same-origin", cache: "no-store" }),
  reload: (): void => window.location.reload(),
};

// waitForGateway polls the health endpoint until the restarted gateway answers, then reloads the
// page. It calls onGone when the gateway does not come back in time (the new setting may no longer
// reach this device). The returned function stops the polling.
function waitForGateway(onGone: () => void): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = Date.now() + restartTiming.waitMs;
  const poll = async () => {
    if (stopped) {
      return;
    }
    let ok = false;
    try {
      ok = (await restartTiming.health()).ok;
    } catch {
      /* not back yet */
    }
    if (stopped) {
      return;
    }
    if (ok) {
      restartTiming.reload();
      return;
    }
    if (Date.now() >= deadline) {
      onGone();
      return;
    }
    timer = setTimeout(() => void poll(), restartTiming.pollMs);
  };
  timer = setTimeout(() => void poll(), restartTiming.firstPollMs);
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}

// The command that lets the person's user run tailscale serve without sudo; the gateway's error
// names it when tailscale refuses.
const OPERATOR_COMMAND = "sudo tailscale set --operator=$USER";

// CommandCopy is a copy button for a command shown next to it.
function CommandCopy({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      class={smallBtn}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true);
          setTimeout(() => setDone(false), 1800);
        }
      }}
    >
      {choose(done, t("Copied"), t("Copy"))}
    </button>
  );
}

// NetworkErrorText is an error of the network section. When tailscale refused for lack of
// permission, the command that fixes it is set apart, with a copy button and what to do next.
function NetworkErrorText({ text }: { text: string }) {
  if (!text.includes("--operator")) {
    return <>{text}</>;
  }
  const at = text.indexOf("sudo tailscale set");
  const before = choose(at >= 0, text.slice(0, at).trimEnd(), text);
  return (
    <>
      {before}{" "}
      <span class="inline-flex flex-wrap items-center gap-2 mt-1 text-[#e8e8ea]">
        <code class="break-all" data-testid="operator-command">
          {OPERATOR_COMMAND}
        </code>
        <CommandCopy text={OPERATOR_COMMAND} />
      </span>
      <span class="block mt-1 text-[#7a7a8c]">
        {t("Then turn the switch on again.")}
      </span>
    </>
  );
}

// restartLines is what the confirmation spells out before the gateway restarts itself.
function restartLines(): string[] {
  return [
    t("The gateway restarts to apply the saved setting."),
    t(
      "Running work is interrupted for a moment; saved sessions resume after the restart.",
    ),
    t(
      "If the new setting no longer reaches this device (for example, you chose “This machine only” and opened this page from another device), this page will stop working: open it from the gateway's machine.",
    ),
  ];
}

// modeLabel names a network mode the way the choices do.
function modeLabel(m: NetworkMode): string {
  if (m === "local") {
    return t("This machine only");
  }
  if (m === "lan") {
    return t("Local network");
  }
  if (m === "all") {
    return t("Everywhere");
  }
  return t("Custom address");
}

// modeHint says who can connect with a mode, and that this machine alone is the safest.
function modeHint(m: NetworkMode, listen: string): string {
  if (m === "local") {
    return t("Safest: only this machine can open Motita (127.0.0.1).");
  }
  if (m === "lan") {
    return t(
      "Listens on every interface, but only private (local network) addresses may connect. This machine only is the safest choice.",
    );
  }
  if (m === "all") {
    return t(
      "Listens on 0.0.0.0: any address that can reach this machine may connect. This machine only is the safest choice.",
    );
  }
  return tf(
    "Set by hand in the configuration: {listen}. Choosing an option replaces it.",
    { listen },
  );
}

// modeConsequences is what the confirmation spells out before a mode is saved: who gains or
// loses access, and that the token alone guards the gateway once other machines reach it.
function modeConsequences(m: ChosenMode): string[] {
  if (m === "local") {
    return [
      t(
        "Other devices will lose access: only this machine will be able to open Motita.",
      ),
    ];
  }
  const who = choose(
    m === "lan",
    t("Devices on your local network will be able to reach the gateway."),
    t(
      "Anyone who can reach this machine over the network will be able to reach the gateway.",
    ),
  );
  return [
    who,
    t(
      "The gateway speaks plain HTTP: the token travels unencrypted over the network.",
    ),
    t(
      "The token is the only thing protecting a program that runs commands on this machine.",
    ),
  ];
}

// safeHttps is the address when it is an https link, "" otherwise: the page never links to
// anything else the gateway hands it.
function safeHttps(url: string | undefined): string {
  if (url && /^https:\/\/[^\s"'<>]+$/.test(url)) {
    return url;
  }
  return "";
}

// InlineConfirm asks before a network change, inside the card: what it implies, then Cancel and
// Confirm. Cancel takes the focus, so Enter right away never applies the change.
function InlineConfirm({
  id,
  title,
  lines,
  confirmLabel,
  danger,
  busy,
  onConfirm,
  onCancel,
}: {
  id: string;
  title: string;
  lines: string[];
  confirmLabel: string;
  danger: boolean;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    cancelRef.current?.focus();
  }, []);
  return (
    <div
      role="group"
      aria-labelledby={id}
      class="settings-confirm"
      data-testid="network-confirm"
    >
      <div id={id} class="text-sm font-semibold text-[#e8e8ea]">
        {title}
      </div>
      <ul class="settings-confirm-lines">
        {lines.map((l) => (
          <li key={l}>{l}</li>
        ))}
      </ul>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          ref={cancelRef}
          class={smallBtn}
          disabled={busy}
          onClick={onCancel}
        >
          {t("Cancel")}
        </button>
        <button
          type="button"
          class={
            smallBtn +
            choose(
              danger,
              " text-danger",
              " bg-accent text-white border-transparent",
            )
          }
          disabled={busy}
          onClick={onConfirm}
        >
          {confirmLabel}
        </button>
      </div>
    </div>
  );
}

type NetworkPending =
  | { kind: "mode"; mode: ChosenMode }
  | { kind: "tailscale" }
  | { kind: "restart" };

// TailscaleRow is Tailscale's state: how to get it, that it is not connected, or the switch that
// publishes the gateway inside the tailnet with `tailscale serve`.
function TailscaleRow({
  view,
  busy,
  asking,
  onAsk,
  onCancel,
  onOff,
}: {
  view: NetworkView;
  busy: boolean;
  asking: boolean;
  onAsk: () => void;
  onCancel: () => void;
  onOff: () => void;
}) {
  const ts = view.tailscale;
  if (!ts?.installed) {
    return (
      <Row
        id="settings-tailscale"
        label="Tailscale"
        hint={t(
          "Not installed. Tailscale gives you private https access from your own devices, without opening the gateway to the network.",
        )}
      >
        <a
          href={TAILSCALE_DOWNLOAD}
          target="_blank"
          rel="noopener noreferrer"
          class={smallBtn + " inline-flex items-center"}
        >
          {t("Get Tailscale")} ↗
        </a>
      </Row>
    );
  }
  if (!ts.running) {
    return (
      <Row
        id="settings-tailscale"
        label="Tailscale"
        hint={choose(
          ts.state,
          tf("Tailscale is installed but not connected ({state}).", {
            state: ts.state,
          }),
          t("Tailscale is installed but not connected."),
        )}
      >
        {null}
      </Row>
    );
  }
  const url = safeHttps(ts.url);
  let hint: ComponentChildren = t(
    "Only the devices in your tailnet can open it, with https; not the internet.",
  );
  if (ts.serving && url) {
    hint = (
      <>
        {t("Published at")}{" "}
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline break-all"
          data-testid="tailscale-url"
        >
          {url}
        </a>
      </>
    );
  }
  return (
    <Row
      id="settings-tailscale"
      label={t("Publish in my tailnet (https)")}
      hint={hint}
    >
      <label class="settings-switch-hit">
        <input
          type="checkbox"
          role="switch"
          class="settings-switch"
          aria-labelledby="settings-tailscale"
          disabled={busy}
          checked={ts.serving || asking}
          onChange={(e) => {
            const on = (e.target as HTMLInputElement).checked;
            if (on) {
              onAsk();
            } else if (asking) {
              onCancel();
            } else {
              onOff();
            }
          }}
        />
      </label>
    </Row>
  );
}

// NetworkSettings is how the gateway is exposed: the address it listens on (saved in the
// configuration, applied at the next start) and Tailscale. Nothing changes without a confirmation
// that says what it implies. The section stays hidden on a gateway without the network API.
function NetworkSettings({ api }: { api: Api }) {
  const [view, setView] = useState<NetworkView | null | undefined>(undefined);
  const [loadErr, setLoadErr] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<NetworkPending | null>(null);
  // While the gateway restarts: waiting for it, or it did not come back here in time.
  const [restarting, setRestarting] = useState<"" | "waiting" | "gone">("");
  const stopWaiting = useRef<(() => void) | null>(null);

  useEffect(() => () => stopWaiting.current?.(), []);

  useEffect(() => {
    let live = true;
    getNetwork(api)
      .then((v) => {
        if (live) {
          setView(v);
        }
      })
      .catch((e) => {
        if (live) {
          setLoadErr(errorMessage(e));
        }
      });
    return () => {
      live = false;
    };
  }, [api]);

  const run = async (req: () => Promise<NetworkView>) => {
    setBusy(true);
    setErr("");
    try {
      setView(await req());
    } catch (e) {
      setErr(errorMessage(e));
    }
    setPending(null);
    setBusy(false);
  };

  const restart = async () => {
    setBusy(true);
    setErr("");
    try {
      setView(await restartGateway(api));
    } catch (e) {
      setErr(errorMessage(e));
      setPending(null);
      setBusy(false);
      return;
    }
    setPending(null);
    setRestarting("waiting");
    stopWaiting.current = waitForGateway(() => {
      stopWaiting.current = null;
      setRestarting("gone");
      setBusy(false);
    });
  };

  if (view === null || (view === undefined && !loadErr)) {
    return null;
  }
  let shown: NetworkMode = view?.mode ?? "custom";
  if (pending?.kind === "mode") {
    shown = pending.mode;
  }
  const pickMode = (m: NetworkMode) => {
    if (m === "custom" || m === view?.mode) {
      setPending(null);
      return;
    }
    setErr("");
    setPending({ kind: "mode", mode: m });
  };
  return (
    <section class="settings-group" aria-labelledby="settings-network-h">
      <h3 id="settings-network-h" class="settings-group-title">
        {t("Network")}
      </h3>
      <div class="settings-card" data-testid="network-setting">
        {loadErr && (
          <p class="settings-card-note text-xs text-danger" role="alert">
            {loadErr}
          </p>
        )}
        {view && (
          <>
            <Row
              id="settings-network"
              label={t("Who can connect")}
              stack
              hint={
                modeHint(shown, view.listen) +
                choose(
                  view.can_save,
                  "",
                  " " +
                    t(
                      "The gateway runs on its built-in defaults, without a configuration file, so this cannot be changed here.",
                    ),
                )
              }
            >
              <Segmented<NetworkMode>
                name="settings-network"
                labelledBy="settings-network"
                value={shown}
                onChange={pickMode}
                disabled={!view.can_save || busy}
                options={[
                  { value: "local", label: t("This machine only") },
                  { value: "lan", label: t("Local network") },
                  { value: "all", label: t("Everywhere") },
                ]}
              />
            </Row>
            {pending?.kind === "mode" && (
              <InlineConfirm
                id="settings-network-confirm"
                title={tf("Switch to “{mode}”?", {
                  mode: modeLabel(pending.mode),
                })}
                lines={[
                  ...modeConsequences(pending.mode),
                  t(
                    "The change is saved to the configuration and applies when the gateway restarts: run motita gateway stop, then motita gateway start.",
                  ),
                ]}
                confirmLabel={t("Confirm")}
                danger={pending.mode !== "local"}
                busy={busy}
                onConfirm={() =>
                  void run(() => setNetworkMode(api, pending.mode))
                }
                onCancel={() => setPending(null)}
              />
            )}
            {view.restart_needed && (
              <div
                class="settings-row"
                role="status"
                data-testid="network-restart"
              >
                <div class="settings-row-text">
                  <div class="text-sm text-warning">
                    {t("Saved. Applies when the gateway restarts")}
                  </div>
                  <div class="text-xs text-[#7a7a8c] mt-0.5">
                    {choose(
                      view.can_restart,
                      tf("Running now: {mode} ({listen}).", {
                        mode: modeLabel(view.active_mode),
                        listen: view.active_listen,
                      }),
                      tf(
                        "Running now: {mode} ({listen}). Restart it with motita gateway stop, then motita gateway start.",
                        {
                          mode: modeLabel(view.active_mode),
                          listen: view.active_listen,
                        },
                      ),
                    )}
                  </div>
                </div>
                {view.can_restart && (
                  <div class="settings-row-control">
                    <button
                      type="button"
                      class={smallBtn}
                      disabled={busy || restarting === "waiting"}
                      aria-expanded={pending?.kind === "restart"}
                      data-testid="network-restart-now"
                      onClick={() => {
                        setErr("");
                        setRestarting("");
                        setPending({ kind: "restart" });
                      }}
                    >
                      {t("Restart now")}
                    </button>
                  </div>
                )}
              </div>
            )}
            {pending?.kind === "restart" && (
              <InlineConfirm
                id="settings-restart-confirm"
                title={t("Restart the gateway now?")}
                lines={restartLines()}
                confirmLabel={t("Restart")}
                danger={false}
                busy={busy}
                onConfirm={() => void restart()}
                onCancel={() => setPending(null)}
              />
            )}
            {restarting && (
              <p
                class={
                  "settings-card-note text-xs " +
                  choose(restarting === "gone", "text-danger", "text-warning")
                }
                role="status"
                data-testid="network-restarting"
              >
                {choose(
                  restarting === "gone",
                  t(
                    "The gateway did not come back here. If you changed who can reach it, open it from an address that the new setting allows.",
                  ),
                  t("Restarting the gateway…"),
                )}
              </p>
            )}
            <TailscaleRow
              view={view}
              busy={busy}
              asking={pending?.kind === "tailscale"}
              onAsk={() => {
                setErr("");
                setPending({ kind: "tailscale" });
              }}
              onCancel={() => setPending(null)}
              onOff={() => void run(() => setTailscale(api, false))}
            />
            {pending?.kind === "tailscale" && (
              <InlineConfirm
                id="settings-tailscale-confirm"
                title={t("Publish in your tailnet?")}
                lines={[
                  t(
                    "This runs tailscale serve: the gateway is published with https only to the devices in your tailnet, not to the internet.",
                  ),
                  t("Anyone on those devices still needs the token to get in."),
                ]}
                confirmLabel={t("Publish")}
                danger={false}
                busy={busy}
                onConfirm={() => void run(() => setTailscale(api, true))}
                onCancel={() => setPending(null)}
              />
            )}
            {view.tailscale?.error && (
              <p class="settings-card-note text-xs text-[#7a7a8c]">
                <NetworkErrorText
                  text={tf("Tailscale: {error}", {
                    error: view.tailscale.error,
                  })}
                />
              </p>
            )}
          </>
        )}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="network-error"
          >
            <NetworkErrorText text={err} />
          </p>
        )}
      </div>
    </section>
  );
}

// The icons of the tabs.
function IconSliders() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6" />
    </svg>
  );
}
function IconChip() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <rect x="6" y="6" width="12" height="12" rx="2" />
      <path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4" />
    </svg>
  );
}
function IconBranch() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <circle cx="6" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <circle cx="18" cy="6" r="3" />
      <path d="M6 9v6M18 9a9 9 0 0 1-9 9" />
    </svg>
  );
}

// SettingsModal is the settings dialog. On a screen with room it is a centred window with the
// sections listed on the left; on a phone it takes the whole screen and the sections become tabs
// across the top. The layout lives in index.css (.settings-panel), keyed to the md breakpoint.
export function SettingsModal({
  langSetting,
  langAvailable,
  onLang,
  onClose,
  git,
  voice,
  llm,
  network,
}: Props) {
  const [tab, setTab] = useState<Tab>("general");
  const ref = useRef<HTMLDialogElement>(null);
  useDialog(ref, onClose);
  const tabs: { id: Tab; label: string; icon: ComponentChildren }[] = [
    { id: "general", label: t("General"), icon: <IconSliders /> },
  ];
  if (llm) {
    tabs.push({ id: "providers", label: t("Providers"), icon: <IconChip /> });
  }
  if (git) {
    tabs.push({ id: "git", label: t("Git connections"), icon: <IconBranch /> });
  }
  // Arrow keys move between the tabs, as in any tab list.
  const onTabKey = (e: KeyboardEvent) => {
    const keys = ["ArrowLeft", "ArrowUp", "ArrowRight", "ArrowDown"];
    const k = keys.indexOf(e.key);
    if (k < 0) {
      return;
    }
    e.preventDefault();
    const i = tabs.findIndex((x) => x.id === tab);
    const next = tabs[(i + choose(k < 2, tabs.length - 1, 1)) % tabs.length];
    setTab(next.id);
    ref.current
      ?.querySelector<HTMLElement>(`#settings-tab-${next.id}`)
      ?.focus();
  };
  return (
    <ModalBackdrop onClose={onClose} class="settings-backdrop">
      <dialog
        open
        ref={ref}
        data-dialog-root
        data-testid="settings-modal"
        class="modal-dialog settings-panel frosted"
        aria-modal="true"
        aria-labelledby="settings-title"
      >
        <div class="settings-head">
          <h2
            id="settings-title"
            class="flex-1 text-base font-semibold text-[#e8e8ea]"
          >
            {t("Settings")}
          </h2>
          <button
            class="p-1.5 rounded-lg hover:bg-white/5 text-[#9a9aaa]"
            aria-label={t("Close")}
            onClick={onClose}
          >
            <IconClose size={18} />
          </button>
        </div>
        {tabs.length > 1 && (
          <div
            role="tablist"
            tabIndex={-1}
            aria-label={t("Settings")}
            class="settings-nav"
            onKeyDown={onTabKey}
          >
            {tabs.map((x) => (
              <button
                key={x.id}
                id={`settings-tab-${x.id}`}
                type="button"
                role="tab"
                class="settings-tab"
                aria-selected={tab === x.id}
                aria-controls="settings-pane"
                tabIndex={choose(tab === x.id, 0, -1)}
                onClick={() => setTab(x.id)}
              >
                {x.icon}
                {x.label}
              </button>
            ))}
          </div>
        )}
        <div
          id="settings-pane"
          role="tabpanel"
          class="settings-pane"
          aria-labelledby={`settings-tab-${tab}`}
        >
          {tab === "general" && (
            <GeneralSettings
              langSetting={langSetting}
              langAvailable={langAvailable}
              onLang={onLang}
              voice={voice}
              network={network}
            />
          )}
          {tab === "providers" && llm && <ProviderSettings {...llm} />}
          {tab === "git" && git && <GitConnections {...git} />}
        </div>
      </dialog>
    </ModalBackdrop>
  );
}

const inputCls =
  "min-w-0 px-2 py-1.5 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const smallBtn =
  "btn-outline px-3 min-h-[32px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform";

// The logo of each git host, from Simple Icons (CC0), in the host's own colour. Gitea, Forgejo and
// any host the page does not know get the generic branch icon.
const FORGE_LOGOS: Record<string, { color: string; d: string }> = {
  github: {
    color: "#e8e8ea",
    d: "M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12",
  },
  gitlab: {
    color: "#fc6d26",
    d: "m23.6 9.593-.033-.086L20.3.98a.851.851 0 0 0-.336-.405.875.875 0 0 0-1 .054.875.875 0 0 0-.29.44L16.47 7.818H7.537L5.333 1.07a.857.857 0 0 0-.29-.441.875.875 0 0 0-1-.054.859.859 0 0 0-.336.405L.433 9.502l-.032.086a6.066 6.066 0 0 0 2.012 7.01l.01.009.03.021 4.977 3.727 2.462 1.863 1.5 1.132a1.008 1.008 0 0 0 1.22 0l1.499-1.132 2.461-1.863 5.006-3.75.013-.01a6.068 6.068 0 0 0 2.01-7.002z",
  },
  bitbucket: {
    color: "#2684ff",
    d: "M.778 1.213a.768.768 0 00-.768.892l3.263 19.81c.084.5.515.868 1.022.873H19.95a.772.772 0 00.77-.646l3.27-20.03a.768.768 0 00-.768-.891zM14.52 15.53H9.522L8.17 8.466h7.561z",
  },
};

// ForgeLogo is the logo of a known git host, or the generic branch icon.
function ForgeLogo({ kind }: { kind: string }) {
  const logo = FORGE_LOGOS[kind];
  if (!logo) {
    return <IconBranch />;
  }
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill={logo.color}>
      <path d={logo.d} />
    </svg>
  );
}

// ForgeIcon is the logo of an account's git host, on a small tile at the start of its row.
function ForgeIcon({ kind }: { kind: string }) {
  return (
    <span class="git-account-icon" aria-hidden="true" data-kind={kind}>
      <ForgeLogo kind={kind} />
    </span>
  );
}

// GitConnections lists every git host with its state, and connects, disconnects or adds one.
function GitConnections({
  api,
  rev,
  onConnect,
  onNotice,
}: NonNullable<Props["git"]>) {
  const [accounts, setAccounts] = useState<GitAccount[] | null>(null);
  const [err, setErr] = useState("");
  const [confirm, setConfirm] = useState("");
  const [adding, setAdding] = useState(false);
  const [kind, setKind] = useState("gitlab");
  const [host, setHost] = useState("");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let live = true;
    listAccounts(api)
      .then((a) => {
        if (live) {
          setAccounts(a);
          setErr("");
        }
      })
      .catch((e) => {
        if (live) {
          setErr(errorMessage(e));
          setAccounts([]);
        }
      });
    return () => {
      live = false;
    };
  }, [rev, reload]);

  const doDisconnect = async (a: GitAccount) => {
    try {
      await disconnect(api, a.id);
      onNotice(tf("Disconnected from {host}", { host: a.name }));
    } catch (e) {
      onNotice(errorMessage(e), true);
    }
    setConfirm("");
    setReload((n) => n + 1);
  };

  const cleanHost = trimTrailingSlashes(
    host.trim().replace(/^https?:\/\//, ""),
  );
  return (
    <section
      class="settings-group"
      aria-labelledby="git-conn-h"
      data-testid="git-connections"
    >
      {/* The tab already names the section on screen; the heading stays for screen readers. */}
      <h3 id="git-conn-h" class="sr-only">
        {t("Git connections")}
      </h3>
      <p class="text-xs text-[#7a7a8c] mb-3">
        {t(
          "Connect a git host to pick your repositories and clone private ones.",
        )}
      </p>
      {accounts === null && (
        <output class="block text-xs text-[#9a9aaa]">{t("Loading…")}</output>
      )}
      {err && (
        <p class="text-xs text-danger mb-2" role="alert">
          {err}
        </p>
      )}
      {!!accounts?.length && (
        <ul class="settings-card">
          {accounts.map((a) => (
            <li key={a.id} class="settings-row git-account">
              <ForgeIcon kind={a.kind} />
              <div class="settings-row-text">
                <div class="text-sm text-[#e8e8ea] truncate">
                  {a.name} <span class="text-xs text-[#7a7a8c]">{a.host}</span>
                </div>
                <div
                  class={
                    "git-account-state text-xs " +
                    choose(a.connected, "text-[#5fd08a]", "text-[#7a7a8c]")
                  }
                >
                  {choose(
                    a.connected,
                    tf("Connected as {user}", { user: a.username || "…" }),
                    t("Not connected"),
                  )}
                </div>
              </div>
              <div class="settings-row-control git-account-actions">
                {confirm === a.id && (
                  <>
                    <button
                      class={smallBtn + " text-danger"}
                      onClick={() => doDisconnect(a)}
                    >
                      {t("Confirm")}
                    </button>
                    <button class={smallBtn} onClick={() => setConfirm("")}>
                      {t("Cancel")}
                    </button>
                  </>
                )}
                {confirm !== a.id && a.connected && (
                  <>
                    <button
                      class={smallBtn}
                      aria-label={tf("Reconnect {host}", { host: a.name })}
                      onClick={() => onConnect(a.id)}
                    >
                      {t("Reconnect")}
                    </button>
                    <button
                      class={smallBtn}
                      aria-label={tf("Disconnect {host}", { host: a.name })}
                      onClick={() => setConfirm(a.id)}
                    >
                      {t("Disconnect")}
                    </button>
                  </>
                )}
                {confirm !== a.id && !a.connected && (
                  <button
                    class={
                      smallBtn + " bg-accent text-white border-transparent"
                    }
                    aria-label={tf("Connect {host}", { host: a.name })}
                    onClick={() => onConnect(a.id)}
                  >
                    {t("Connect")}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {adding && (
        <form
          class="settings-card settings-add space-y-2 mt-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (cleanHost) {
              onConnect(undefined, { kind, host: cleanHost });
              setAdding(false);
              setHost("");
            }
          }}
        >
          <div class="settings-add-fields">
            <select
              class={inputCls}
              value={kind}
              aria-label={t("Server type")}
              onChange={(e) => setKind((e.target as HTMLSelectElement).value)}
            >
              <option value="gitlab">GitLab</option>
              <option value="github">GitHub Enterprise</option>
              <option value="gitea">Gitea / Forgejo</option>
            </select>
            <input
              class={inputCls + " flex-1"}
              value={host}
              placeholder="git.example.com"
              aria-label={t("Server address")}
              autocapitalize="off"
              spellcheck={false}
              onInput={(e) => setHost((e.target as HTMLInputElement).value)}
            />
          </div>
          <div class="flex justify-end gap-2">
            <button
              type="button"
              class={smallBtn}
              onClick={() => setAdding(false)}
            >
              {t("Cancel")}
            </button>
            <button
              type="submit"
              class={smallBtn + " bg-accent text-white border-transparent"}
              disabled={!cleanHost}
            >
              {t("Continue")}
            </button>
          </div>
        </form>
      )}
      {!adding && (
        <button class={smallBtn + " mt-3"} onClick={() => setAdding(true)}>
          {t("Add self-hosted server")}
        </button>
      )}
    </section>
  );
}
