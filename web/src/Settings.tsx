import { useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { ModalBackdrop } from "./ModalBackdrop";
import { useDialog } from "./useDialog";
import { IconClose } from "./icons";
import { InsecureWarning } from "./InsecureWarning";
import "./dialog.css";
import { t } from "./i18n";
import { initSettings } from "./settings";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import {
  ProviderSettings,
  type ProviderSettingsProps,
} from "./ProviderSettings";
import {
  AppearanceSettings,
  type AppearanceSettingsProps,
} from "./AppearanceSettings";
import { NotifySetting } from "./NotifySettings";
import {
  SpeechSettings,
  VoiceSettings,
  type SpeechSettingsProps,
  type VoiceSettingsProps,
} from "./VoiceSettings";
import { NetworkSettings } from "./NetworkSettings";
import { GitConnections, type GitSettingsProps } from "./GitConnections";
import {
  IconBell,
  IconBranch,
  IconChip,
  IconGlobe,
  IconMic,
  IconSliders,
} from "./SettingsIcons";

initSettings();

export { InsecureWarning };
export { unblockSteps } from "./NotifySettings";
export { restartTiming } from "./NetworkSettings";
export type { SpeechSettingsProps, VoiceSettingsProps } from "./VoiceSettings";

interface Props extends AppearanceSettingsProps {
  onClose: () => void;
  // The git host connections section. Absent on a gateway that has no git API.
  git?: GitSettingsProps;
  // Speech input. Absent where the page has no voice store (the tests of other sections).
  voice?: VoiceSettingsProps;
  // Reading answers aloud. Absent where the page has no speech store.
  speech?: SpeechSettingsProps;
  // The LLM providers and the default model. Absent where the page has no API to reach them.
  llm?: ProviderSettingsProps;
  // How the gateway is exposed on the network. Absent where the page has no API to hand it, and
  // the section stays hidden on a gateway without the network API.
  network?: { api: Api };
  // Phone notifications through the gateway (Web Push). Absent where the page has no API to hand.
  push?: { api: Api };
  // tab is the tab the dialog opens on; the first one when absent.
  tab?: Tab;
}

// The tabs: one per kind of setting, so each is found where its name says.
export type Tab =
  | "appearance"
  | "notifications"
  | "voice"
  | "network"
  | "providers"
  | "git";

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
  speech,
  llm,
  network,
  push,
  tab: firstTab = "appearance",
}: Props) {
  const [tab, setTab] = useState<Tab>(firstTab);
  const ref = useRef<HTMLDialogElement>(null);
  useDialog(ref, onClose);
  const tabs: { id: Tab; label: string; icon: ComponentChildren }[] = [
    { id: "appearance", label: t("Appearance"), icon: <IconSliders /> },
    {
      id: "notifications",
      label: t("Notifications"),
      icon: <IconBell />,
    },
  ];
  if (voice || speech) {
    tabs.push({ id: "voice", label: t("Voice"), icon: <IconMic /> });
  }
  if (network) {
    tabs.push({ id: "network", label: t("Network"), icon: <IconGlobe /> });
  }
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
          {tab === "appearance" && (
            <AppearanceSettings
              langSetting={langSetting}
              langAvailable={langAvailable}
              onLang={onLang}
            />
          )}
          {tab === "notifications" && <NotifySetting push={push} />}
          {tab === "voice" && (
            <>
              {voice && <VoiceSettings {...voice} />}
              {speech && <SpeechSettings {...speech} />}
            </>
          )}
          {tab === "network" && network && (
            <NetworkSettings api={network.api} />
          )}
          {tab === "providers" && llm && <ProviderSettings {...llm} />}
          {tab === "git" && git && <GitConnections {...git} />}
        </div>
      </dialog>
    </ModalBackdrop>
  );
}
