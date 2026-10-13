import { useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { ModalBackdrop } from "./ModalBackdrop";
import { useDialog } from "./useDialog";
import { IconAlert, IconClose } from "./icons";
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

initSettings();

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

// InsecureWarning is the amber pill a setting shows when the page was opened without a secure
// connection, so the browser hides the feature. It says that in plain words; the technical reason
// (https or localhost) and the way out stay folded under "Why?" for whoever wants them.
export function InsecureWarning({
  text,
  guide,
}: {
  text: string;
  guide: string;
}) {
  return (
    <div class="settings-warn" role="note" data-testid="insecure-warning">
      <div class="settings-warn-title">
        <IconAlert size={14} class="settings-warn-icon" />
        {t("Connection not secure")}
      </div>
      <p class="settings-warn-text">{text}</p>
      <details class="settings-warn-more">
        <summary>{t("Why?")}</summary>
        <p>
          {t(
            "Browsers only allow it on a secure address: one that starts with https://, or localhost on this same computer. Open Motita through https to use it.",
          )}{" "}
          <a href={guide} target="_blank" rel="noopener noreferrer">
            {t("How to set it up")}
          </a>
        </p>
      </details>
    </div>
  );
}

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
            onChange={(mic) => setPrefs({ ...prefs, mic })}
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
            onChange={(listen) => setPrefs({ ...prefs, listen })}
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
      installed
        ? t("Open the app's menu (⋮ or ⋯ at the top), then App info.")
        : t("Click the icon to the left of the address."),
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

type Tab = "general" | "git";

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
}: {
  name: string;
  labelledBy: string;
  value: T;
  options: Choice<T>[];
  onChange: (v: T) => void;
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
  warning,
  children,
}: {
  id: string;
  label: string;
  hint?: string;
  warning?: ComponentChildren;
  children: ComponentChildren;
}) {
  return (
    <div class="settings-row">
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
}: Pick<Props, "langSetting" | "langAvailable" | "onLang" | "voice">) {
  const [s, setS] = useState<UISettings>(() => initSettings());
  const update = (next: UISettings) => {
    setS(next);
    saveSettings(next);
  };
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
              onChange={(fontSize) => update({ ...s, fontSize })}
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
              onChange={(density) => update({ ...s, density })}
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

// NotifySetting is the desktop notifications for a CI that ends: a button that asks the browser while it has not been asked,
// the steps to allow them again once refused, and the user's own on/off switch once allowed.
function NotifySetting() {
  const [notifyOn, setNotifyOn] = useState(() => loadNotify());
  // The browser's answer is read on every render: it can change in the browser's own settings
  // while this is open, so a permission change and coming back to the tab both draw it again.
  const [, bump] = useState(0);
  const [asked, setAsked] = useState(false);
  const [tested, setTested] = useState<string | undefined>();
  useEffect(() => {
    const again = () => bump((n) => n + 1);
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
  }, []);
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
  const showSteps = ns === "blocked" || (ns === "ask" && asked);
  const ask = () => {
    let p: Promise<NotificationPermission> | undefined;
    try {
      p = Notification.requestPermission();
    } catch {
      /* Safari before 15 only takes a callback; the focus listener redraws */
    }
    void p
      ?.then((r) => {
        if (r === "granted") {
          setNotifyOn(true);
          saveNotify(true);
        }
      })
      .catch(() => {
        /* ignore */
      })
      .finally(() => {
        setAsked(true);
        bump((n) => n + 1);
      });
  };
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
          hint={
            ns === "ask" && asked
              ? t("The browser did not ask. To allow them by hand:")
              : notifyHint(ns, platform)
          }
        >
          {ns === "ask" && (
            <button type="button" class={smallBtn} onClick={ask}>
              {t("Allow notifications")}
            </button>
          )}
          {ns === "blocked" && (
            <button
              type="button"
              class={smallBtn}
              onClick={() => bump((n) => n + 1)}
            >
              {t("Check again")}
            </button>
          )}
          {(ns === "on" || ns === "off") && (
            <label class="settings-switch-hit">
              <input
                type="checkbox"
                role="switch"
                class="settings-switch"
                aria-labelledby="settings-notify"
                checked={ns === "on"}
                onChange={(e) => {
                  const v = (e.target as HTMLInputElement).checked;
                  setNotifyOn(v);
                  saveNotify(v);
                }}
              />
            </label>
          )}
        </Row>
        {ns === "on" && (
          <Row
            id="settings-notify-test"
            label={t("Test notification")}
            hint={tested ?? t("Shows one now, to check that they arrive.")}
          >
            <button
              type="button"
              class={smallBtn}
              onClick={() => {
                void showNotification(t("Motita notifications work"), {
                  body: t("This is how a CI that ends will look."),
                  tag: "motita-test",
                }).then((ok) =>
                  setTested(
                    ok
                      ? t(
                          "Sent. If nothing showed up, check the system's do-not-disturb mode.",
                        )
                      : t("The browser did not show it."),
                  ),
                );
              }}
            >
              {t("Send")}
            </button>
          </Row>
        )}
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

// The icons of the two tabs.
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
}: Props) {
  const [tab, setTab] = useState<Tab>("general");
  const ref = useRef<HTMLDialogElement>(null);
  useDialog(ref, onClose);
  const tabs: { id: Tab; label: string; icon: ComponentChildren }[] = [
    { id: "general", label: t("General"), icon: <IconSliders /> },
  ];
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
            />
          )}
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

// ForgeIcon is the logo of an account's git host, on a small tile at the start of its row.
function ForgeIcon({ kind }: { kind: string }) {
  const logo = FORGE_LOGOS[kind];
  return (
    <span class="git-account-icon" aria-hidden="true" data-kind={kind}>
      {logo ? (
        <svg width="18" height="18" viewBox="0 0 24 24" fill={logo.color}>
          <path d={logo.d} />
        </svg>
      ) : (
        <IconBranch />
      )}
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
