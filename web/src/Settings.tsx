import { useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { ModalBackdrop } from "./ModalBackdrop";
import { useDialog } from "./useDialog";
import { IconClose } from "./icons";
import "./dialog.css";
import { t } from "./i18n";
import { initSettings } from "./settings";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import {
  ProviderSettings,
  type ProviderSettingsProps,
} from "./ProviderSettings";
import { ModelSettings } from "./ModelSettings";
import {
  AppearanceSettings,
  type AppearanceSettingsProps,
} from "./AppearanceSettings";
import { NotifySetting } from "./NotifySettings";
import { VoiceSettings, type VoiceSettingsProps } from "./VoiceSettings";
import { SpeechSettings, type SpeechSettingsProps } from "./SpeechSettings";
import { NetworkSettings } from "./NetworkSettings";
import { SecuritySettings } from "./SecuritySettings";
import { GitConnections, type GitSettingsProps } from "./GitConnections";
import {
  IconBell,
  IconBranch,
  IconChip,
  IconGlobe,
  IconMic,
  IconPlug,
  IconShield,
  IconSliders,
} from "./SettingsIcons";

initSettings();

export { InsecureWarning } from "./InsecureWarning";
export { unblockSteps } from "./NotifySettings";
export { restartTiming } from "./NetworkSettings";
export type { VoiceSettingsProps } from "./VoiceSettings";
export type { SpeechSettingsProps } from "./SpeechSettings";

interface Props extends AppearanceSettingsProps {
  onClose: () => void;
  // The git host connections section. Absent on a gateway that has no git API.
  git?: GitSettingsProps;
  // Speech input. Absent where the page has no voice store (the tests of other sections).
  voice?: VoiceSettingsProps;
  // Reading answers aloud. Absent where the page has no speech store.
  speech?: SpeechSettingsProps;
  // The LLM providers (their tab) and the models each kind of work runs on (theirs). Absent where
  // the page has no API to reach them.
  llm?: ProviderSettingsProps;
  // How the gateway is exposed on the network. Absent where the page has no API to hand it, and
  // the section stays hidden on a gateway without the network API.
  network?: { api: Api };
  // How much of the disk the agent reaches. Absent where the page has no API to hand it, and the
  // section stays hidden on a gateway without the security API.
  security?: { api: Api };
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
  | "security"
  | "network"
  | "providers"
  | "models"
  | "git";

interface TabInfo {
  id: Tab;
  label: string;
  icon: ComponentChildren;
}

// settingsTabs are the tabs the dialog shows: the two every page has, then one per section the
// page was handed.
function settingsTabs(p: Props): TabInfo[] {
  const tabs: TabInfo[] = [
    { id: "appearance", label: t("Appearance"), icon: <IconSliders /> },
    {
      id: "notifications",
      label: t("Notifications"),
      icon: <IconBell />,
    },
  ];
  if (p.voice || p.speech) {
    tabs.push({ id: "voice", label: t("Voice"), icon: <IconMic /> });
  }
  if (p.security) {
    tabs.push({ id: "security", label: t("Security"), icon: <IconShield /> });
  }
  if (p.network) {
    tabs.push({ id: "network", label: t("Network"), icon: <IconGlobe /> });
  }
  if (p.llm) {
    tabs.push(
      { id: "providers", label: t("Providers"), icon: <IconPlug /> },
      { id: "models", label: t("Models"), icon: <IconChip /> },
    );
  }
  if (p.git) {
    tabs.push({ id: "git", label: t("Git connections"), icon: <IconBranch /> });
  }
  return tabs;
}

// VoiceTab is speech input and reading aloud, each where the page has it.
function VoiceTab({ voice, speech }: Pick<Props, "voice" | "speech">) {
  return (
    <>
      {voice && <VoiceSettings {...voice} />}
      {speech && <SpeechSettings {...speech} />}
    </>
  );
}

// PANES draw the content of each tab.
const PANES: Record<Tab, (p: Props) => ComponentChildren> = {
  appearance: (p) => (
    <AppearanceSettings
      langSetting={p.langSetting}
      langAvailable={p.langAvailable}
      onLang={p.onLang}
    />
  ),
  notifications: (p) => <NotifySetting push={p.push} />,
  voice: (p) => <VoiceTab voice={p.voice} speech={p.speech} />,
  security: (p) => p.security && <SecuritySettings api={p.security.api} />,
  network: (p) => p.network && <NetworkSettings api={p.network.api} />,
  providers: (p) => p.llm && <ProviderSettings {...p.llm} />,
  models: (p) => p.llm && <ModelSettings {...p.llm} />,
  git: (p) => p.git && <GitConnections {...p.git} />,
};

// SettingsModal is the settings dialog. On a screen with room it is a centred window with the
// sections listed on the left; on a phone it takes the whole screen and the sections become tabs
// across the top. The layout lives in index.css (.settings-panel), keyed to the md breakpoint.
export function SettingsModal(props: Props) {
  const { onClose } = props;
  const [tab, setTab] = useState<Tab>(props.tab ?? "appearance");
  const ref = useRef<HTMLDialogElement>(null);
  useDialog(ref, onClose);
  const tabs = settingsTabs(props);
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
          {PANES[tab](props)}
        </div>
      </dialog>
    </ModalBackdrop>
  );
}
