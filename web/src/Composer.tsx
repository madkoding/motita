// The composer: solid opaque background, no decorative gradient.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { tagGroupClass } from "./appFormat";
import { IconClose } from "./icons";
import type { SlashCommand } from "./appTypes";
import { TASK_INPUT_ID } from "./appConst";

// The colour of a command's name in the popup, by its group.
const GROUP_COLOR: Record<string, string> = {
  mode: "text-accent",
  action: "text-[#f0a040]",
  session: "text-[#40f0a0]",
  meta: "text-[#a0a0f0]",
};

// SlashItem is one command the popup offers.
function SlashItem({ c, i }: { c: SlashCommand; i: number }) {
  const { slashPopup, setSlashPopup, pickSlash } = useApp().composer;
  const index = slashPopup?.index;
  return (
    <button
      type="button"
      tabIndex={-1}
      class={`plain-btn flex items-center gap-2 px-3 py-2 cursor-pointer transition-colors ${choose(i === index, "bg-white/10", "")}`}
      onClick={() => pickSlash(c)}
      onMouseEnter={() => setSlashPopup((p) => p && { ...p, index: i })}
    >
      <span
        class={`font-mono text-sm font-semibold flex-none w-20 ${GROUP_COLOR[c.group] || "text-[#e8e8ea]"}`}
      >
        {c.name}
      </span>
      {!!c.aliases?.length && (
        <span class="text-xs text-[#7a7a8c] flex-none">
          {c.aliases.join(", ")}
        </span>
      )}
      <span class="flex-1 min-w-0 truncate text-xs text-[#9a9aaa]">
        {t(c.help)}
      </span>
    </button>
  );
}

// SlashPopup is the slash command autocomplete, positioned above the textarea and inside the form
// so it tracks the input position.
function SlashPopup({ items }: { items: SlashCommand[] }) {
  return (
    <div
      class="absolute bottom-full left-0 right-0 mb-1 rounded-xl border border-white/10 shadow-2xl overflow-hidden z-30 max-h-[160px] overflow-y-auto"
      style="background: rgba(10, 10, 16, 0.95); backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);"
    >
      {items.map((c, i) => (
        <SlashItem key={c.name} c={c} i={i} />
      ))}
    </div>
  );
}

// TagPills are the slash commands that are active, shown as pills before the text.
function TagPills() {
  const { activeTags, removeTag } = useApp().composer;
  return (
    <>
      {activeTags.map((tag) => (
        <span
          key={tag.key}
          class={`inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-mono font-semibold flex-none ${tagGroupClass(
            tag.group,
          )}`}
        >
          {tag.name}
          <button
            type="button"
            class="ml-0.5 opacity-60 hover:opacity-100"
            onClick={() => removeTag(tag.key)}
          >
            <IconClose size={12} stroke="2.5" />
          </button>
        </span>
      ))}
    </>
  );
}

// SendButton sends the message, or runs the line in the shell when it is a shell command.
function SendButton({ disabled }: { disabled: boolean }) {
  const { shellMode } = useApp().composer;
  return (
    <button
      type="submit"
      disabled={disabled}
      title={choose(shellMode, t("Run in the shell"), undefined)}
      aria-label={choose(shellMode, t("Run in the shell"), t("Send"))}
      class={`min-h-[44px] min-w-[44px] px-5 sm:px-6 rounded-2xl text-white font-semibold disabled:opacity-40 active:scale-95 transition-transform flex items-center justify-center ${choose(shellMode, "send-shell", "bg-accent")}`}
    >
      {shellMode && (
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" y1="19" x2="20" y2="19" />
        </svg>
      )}
      {!shellMode && (
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <line x1="22" y1="2" x2="11" y2="13" />
          <polygon points="22 2 15 22 11 13 2 9 22 2" />
        </svg>
      )}
    </button>
  );
}

// placeholderOf is what the empty field says.
function placeholderOf(isMerged: boolean, tagged: boolean): string {
  if (isMerged) {
    return t("This session is read-only after integration");
  }
  return t(choose(tagged, "argument…", "Ask for something…"));
}

export function Composer() {
  const { composer, isMerged, rs } = useApp();
  const { slashPopup, input } = composer;
  return (
    <form
      class="frosted flex gap-2 px-3 py-2.5 sm:px-5 sm:py-3 border-t border-white/5 flex-none z-10 relative"
      onSubmit={composer.handleSubmit}
    >
      {isMerged && (
        <span class="inline-flex items-center gap-1.5 px-2.5 py-2 rounded-xl text-xs font-semibold bg-[#f0a040]/15 text-[#f0a040] border border-[#f0a040]/20 flex-none">
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M12 2v20M2 12h20" />
          </svg>
          {t("Merged — Read Only")}
        </span>
      )}
      {slashPopup && <SlashPopup items={slashPopup.items} />}

      <label htmlFor={TASK_INPUT_ID} class="sr-only">
        {t("Task")}
      </label>
      <div class="flex-1 flex flex-wrap items-center gap-1.5 min-h-[44px] max-h-[120px] p-2 rounded-2xl bg-black/30 border border-white/5 focus-within:border-accent backdrop-blur-xs overflow-y-auto">
        <TagPills />
        <textarea
          id={TASK_INPUT_ID}
          rows={1}
          value={input}
          disabled={isMerged}
          onInput={(e) =>
            composer.onInput((e.target as HTMLTextAreaElement).value)
          }
          onKeyDown={composer.handleKeydown}
          placeholder={placeholderOf(isMerged, composer.activeTags.length > 0)}
          class="flex-1 min-h-[28px] max-h-[100px] px-1 py-1 bg-transparent border-0 outline-hidden ring-0 text-[#e8e8ea] resize-none focus:outline-hidden focus:ring-0 focus:border-0 font-sans text-[14px] leading-relaxed disabled:opacity-50 disabled:cursor-not-allowed"
        />
      </div>
      <SendButton disabled={rs.running || isMerged} />
    </form>
  );
}
