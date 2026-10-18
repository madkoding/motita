// The header of the conversation, frosted glass over the background image.
//
// A phone is where this row runs out of room: title, status pill and
// provider/model pill all want width, and the pills used to cover the title
// whole. Below the sm breakpoint the header wraps to two rows -- the title on
// the first, the pills together on the second -- and from sm up it is the one
// row it always was. overflow-hidden keeps a long model name from pushing the
// row wider than the viewport, and the session title gives up width first
// (min-w-0 on its group, truncate on the h1). The cat is the sidebar's mark
// and is not repeated here.
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
      class="sm:flex-none flex items-center gap-1 min-w-0 max-sm:max-w-full sm:max-w-none model-pill text-xs max-sm:text-[10px] px-2 sm:px-2.5 py-1 max-sm:py-0 rounded-full bg-black/20 border border-white/5 text-[#9a9aaa] hover:bg-black/30 hover:border-accent/30 transition-colors cursor-pointer"
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
    <header class="frosted flex flex-wrap sm:flex-nowrap items-center gap-x-2 gap-y-1 sm:gap-3 px-3 sm:px-5 py-2.5 max-sm:py-1.5 sm:py-3 border-b border-white/5 flex-none overflow-hidden z-10">
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
        <h1 class="text-sm sm:text-base font-semibold tracking-wide truncate min-w-0">
          {chatTitle(selectedSession)}
        </h1>
      </div>
      <div class="header-pills flex items-center gap-2 sm:gap-3 min-w-0 max-sm:basis-full sm:flex-none">
        {selectedSession?.auto_approve && (
          <button
            class="auto-approve-pill flex-none whitespace-nowrap px-2 py-0.5 rounded-full max-sm:text-[10px]"
            title={t(
              "Every command in this session runs without asking. Click to be asked again.",
            )}
            onClick={shell.stopAutoApprove}
          >
            {t("all allowed")} ✕
          </button>
        )}
        <span
          class={`state-pill flex-none whitespace-nowrap text-xs max-sm:text-[10px] px-2 sm:px-2.5 py-1 max-sm:py-0 rounded-full bg-black/20 border border-white/5 ${status.stateClass}`}
          aria-live="polite"
        >
          {/* Keyed by the state, so each change rises in; the live region itself stays mounted. */}
          <span key={status.stateText} class="state-pill-text">
            {t(status.stateText)}
          </span>
        </span>
        {model.config && !rs.noSession && <ModelPill config={model.config} />}
      </div>
    </header>
  );
}
