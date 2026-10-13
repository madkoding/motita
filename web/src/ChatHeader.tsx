// The header of the conversation, frosted glass over the background image.
//
// A phone is where this row runs out of room: title, status pill and
// provider/model pill all want width, and the two pills cannot shrink.
// overflow-hidden keeps a long model name from pushing the row wider
// than the viewport, the gaps tighten on the smallest screens, and the
// session title -- the only thing here with a real length -- gives up
// width first (min-w-0 on its group, truncate on the h1).
import { t, tf } from "./i18n";
import { useApp } from "./appStore";
import { chatTitle, reasoningOf } from "./appFormat";
import { IconMonitor } from "./icons";
import type { ConfigView } from "./appTypes";

// ModelPill is the session's provider, model and reasoning level; a click opens the switcher.
function ModelPill({ config }: { config: ConfigView }) {
  const { openModelSwitcher } = useApp().model;
  const level = reasoningOf(config);
  return (
    <button
      class="flex-none flex items-center gap-1 max-w-[38vw] max-[360px]:max-w-[30vw] sm:max-w-none text-xs px-2 sm:px-2.5 py-1 rounded-full bg-black/20 border border-white/5 text-[#9a9aaa] hover:bg-black/30 hover:border-accent/30 transition-colors cursor-pointer"
      title={tf("{provider} / {model} — reasoning: {level}", {
        provider: config.provider,
        model: config.model,
        level: t(level),
      })}
      onClick={openModelSwitcher}
    >
      <IconMonitor size={12} class="text-accent/60 flex-none" />
      <span class="truncate min-w-0">
        {config.provider} / {config.model}
      </span>
      {/* Its own span and flex-none: the model name is what truncates on a narrow
          screen, and the level must survive it. */}
      <span class={`header-reasoning is-${level}`}>{t(level)}</span>
    </button>
  );
}

export function ChatHeader() {
  const { rs, lists, status, model, shell, selectedSession } = useApp();
  return (
    <header class="frosted flex items-center gap-2 sm:gap-3 px-3 sm:px-5 py-2.5 sm:py-3 border-b border-white/5 flex-none overflow-hidden z-10">
      {!lists.sidebarOpen && (
        <button
          class="p-1.5 rounded-lg hover:bg-white/5 transition-colors flex-none"
          onClick={lists.openSidebar}
          aria-label={t("Open sidebar")}
        >
          <svg
            width="22"
            height="22"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <line x1="3" y1="12" x2="21" y2="12" />
            <line x1="3" y1="6" x2="21" y2="6" />
            <line x1="3" y1="18" x2="21" y2="18" />
          </svg>
        </button>
      )}
      <div class="flex items-center gap-1.5 sm:gap-2 min-w-0 flex-1">
        <span class="text-accent text-base sm:text-lg flex-none">🐱</span>
        <h1 class="text-sm sm:text-base font-semibold tracking-wide truncate min-w-0">
          {chatTitle(selectedSession)}
        </h1>
      </div>
      {selectedSession?.auto_approve && (
        <button
          class="auto-approve-pill flex-none whitespace-nowrap px-2 py-0.5 rounded-full"
          title={t(
            "Every command in this session runs without asking. Click to be asked again.",
          )}
          onClick={shell.stopAutoApprove}
        >
          {t("all allowed")} ✕
        </button>
      )}
      <span
        class={`flex-none whitespace-nowrap text-xs px-2 sm:px-2.5 py-1 rounded-full bg-black/20 border border-white/5 ${status.stateClass}`}
        aria-live="polite"
      >
        {t(status.stateText)}
      </span>
      {model.config && !rs.noSession && <ModelPill config={model.config} />}
    </header>
  );
}
