// The composer: solid opaque background, no decorative gradient.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { tagGroupClass } from "./appFormat";
import { IconClose } from "./icons";
import type { SlashCommand } from "./appTypes";
import { TASK_INPUT_ID } from "./appConst";
import { useLayoutEffect, useRef } from "preact/hooks";
import { fitToContent } from "./autosize";
import { filesOf, sizeLabel, thumbOf } from "./attachments";

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

// AttachmentChips are the files attached to the message, each with a way to take it off.
function AttachmentChips() {
  const { attachments, removeAttachment, uploading } = useApp().composer;
  return (
    <>
      {attachments.map((a) => (
        <span
          key={a.path}
          class="inline-flex items-center gap-1 max-w-[200px] px-2 py-0.5 rounded-md text-xs bg-white/10 text-[#e8e8ea] flex-none"
          title={`${a.name} (${sizeLabel(a.size)})`}
          data-testid="attachment-chip"
        >
          {thumbOf(a) && (
            <img
              class="w-5 h-5 rounded-sm object-cover flex-none"
              src={thumbOf(a)}
              alt=""
            />
          )}
          <span class="truncate">{a.name}</span>
          <button
            type="button"
            class="ml-0.5 opacity-60 hover:opacity-100"
            aria-label={t("Remove attachment")}
            onClick={() => removeAttachment(a.path)}
          >
            <IconClose size={12} stroke="2.5" />
          </button>
        </span>
      ))}
      {uploading > 0 && (
        <span
          class="text-xs text-[#9a9aaa] flex-none"
          data-testid="attach-busy"
        >
          {t("Uploading…")}
        </span>
      )}
    </>
  );
}

// AttachButton opens the file picker. Pasting and dropping files onto the composer do the same.
function AttachButton({ disabled }: { disabled: boolean }) {
  const { attachFiles } = useApp().composer;
  const pickerRef = useRef<HTMLInputElement>(null);
  return (
    <>
      <input
        ref={pickerRef}
        type="file"
        multiple
        hidden
        data-testid="attach-input"
        onChange={(e) => {
          const el = e.target as HTMLInputElement;
          void attachFiles(filesOf(el));
          el.value = "";
        }}
      />
      <button
        type="button"
        disabled={disabled}
        onClick={() => pickerRef.current?.click()}
        title={t("Attach files, images or videos")}
        aria-label={t("Attach files, images or videos")}
        data-testid="composer-attach"
        class="composer-attach min-h-[44px] min-w-[44px] rounded-2xl flex items-center justify-center border border-white/10 bg-black/30 text-[#e8e8ea] disabled:opacity-40 active:scale-95 transition-transform"
      >
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
          <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
        </svg>
      </button>
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
      class={`composer-send min-h-[44px] min-w-[44px] px-5 sm:px-6 rounded-2xl text-white font-semibold disabled:opacity-40 active:scale-95 transition-transform flex items-center justify-center ${choose(shellMode, "send-shell", "bg-accent")}`}
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

// micLabel names what the dictation button does now.
function micLabel(phase: string): string {
  if (phase === "recording") {
    return t("Stop and transcribe");
  }
  if (phase === "transcribing") {
    return t("Transcribing…");
  }
  return t("Dictate");
}

// MicButton dictates into the field: one tap records, the next one stops and transcribes. The text
// lands in the field to be read and edited; it is never sent on its own.
function MicButton({ disabled }: { disabled: boolean }) {
  const { voice } = useApp();
  const { phase } = voice;
  return (
    <button
      type="button"
      disabled={disabled || phase === "transcribing"}
      onClick={() => void voice.toggleDictation()}
      title={micLabel(phase)}
      aria-label={micLabel(phase)}
      aria-pressed={phase === "recording"}
      data-testid="composer-mic"
      class={`composer-mic min-h-[44px] min-w-[44px] rounded-2xl flex items-center justify-center border border-white/10 disabled:opacity-40 active:scale-95 transition-transform ${choose(phase === "recording", "bg-[#ff4d6d] text-white animate-pulse", "bg-black/30 text-[#e8e8ea]")}`}
    >
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
        <rect x="9" y="2" width="6" height="12" rx="3" />
        <path d="M5 10a7 7 0 0 0 14 0" />
        <line x1="12" y1="17" x2="12" y2="22" />
      </svg>
    </button>
  );
}

// ListeningBadge says the microphone is open for the wake phrase, for as long as it is.
function ListeningBadge() {
  return (
    <output
      class="absolute -top-3 left-4 inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-semibold bg-[#ff4d6d]/15 text-[#ff8da1] border border-[#ff4d6d]/30"
      data-testid="voice-listening"
    >
      <span
        class="w-2 h-2 rounded-full bg-[#ff4d6d] animate-pulse"
        aria-hidden="true"
      />
      {t("Listening for the wake phrase")}
    </output>
  );
}

// placeholderOf is what the empty field says.
function placeholderOf(
  isMerged: boolean,
  tagged: boolean,
  suggestion: string,
): string {
  if (isMerged) {
    return t("This session is read-only after integration");
  }
  if (suggestion && !tagged) {
    return suggestion + " →";
  }
  return t(choose(tagged, "argument…", "Ask for something…"));
}

export function Composer() {
  const { composer, isMerged, voice } = useApp();
  const { slashPopup, input } = composer;
  const inputRef = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    if (inputRef.current) {
      fitToContent(inputRef.current);
    }
  }, [input]);
  // The text rewraps when the width changes (rotation, the sidebar, a density switch), so the
  // height is fitted again then too. Only a width change counts: the fit itself changes height.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    let width = el.clientWidth;
    const ro = new ResizeObserver(() => {
      if (el.clientWidth !== width) {
        width = el.clientWidth;
        fitToContent(el);
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return (
    <form
      class="frosted flex gap-2 px-3 py-2.5 sm:px-5 sm:py-3 border-t border-white/5 flex-none z-10 relative"
      onSubmit={composer.handleSubmit}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        void composer.attachFiles(filesOf(e.dataTransfer));
      }}
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
      {voice.listening && <ListeningBadge />}
      {composer.attachError && (
        <output
          class="absolute -top-3 right-4 max-w-[80%] truncate px-2 py-0.5 rounded-full text-[11px] font-semibold bg-[#ff4d6d]/15 text-[#ff8da1] border border-[#ff4d6d]/30"
          data-testid="attach-error"
        >
          {composer.attachError}
        </output>
      )}

      <label htmlFor={TASK_INPUT_ID} class="sr-only">
        {t("Task")}
      </label>
      <div class="composer-field flex-1 flex flex-wrap items-center gap-1.5 min-h-[44px] max-h-[120px] p-2 rounded-2xl bg-black/30 border border-white/5 focus-within:border-accent backdrop-blur-xs overflow-y-auto">
        <TagPills />
        <AttachmentChips />
        <textarea
          ref={inputRef}
          id={TASK_INPUT_ID}
          rows={1}
          value={input}
          disabled={isMerged}
          onInput={(e) =>
            composer.onInput((e.target as HTMLTextAreaElement).value)
          }
          onKeyDown={composer.handleKeydown}
          onPaste={(e) => {
            const files = filesOf(e.clipboardData);
            if (files.length > 0) {
              e.preventDefault();
              void composer.attachFiles(files);
            }
          }}
          placeholder={placeholderOf(
            isMerged,
            composer.activeTags.length > 0,
            composer.nextSuggestion,
          )}
          class="flex-1 min-h-[28px] max-h-[100px] px-1 py-1 bg-transparent border-0 outline-hidden ring-0 text-[#e8e8ea] resize-none focus:outline-hidden focus:ring-0 focus:border-0 composer-input font-sans text-[14px] leading-relaxed disabled:opacity-50 disabled:cursor-not-allowed"
        />
      </div>
      {composer.nextSuggestion && input === "" && !isMerged && (
        <button
          type="button"
          class="flex-none px-2.5 rounded-xl text-sm text-[#e8e8ea] bg-white/5 hover:bg-white/10 border border-white/5"
          aria-label={t("Use the suggestion")}
          title={composer.nextSuggestion}
          data-testid="use-suggestion"
          onClick={() => composer.setInput(composer.nextSuggestion)}
        >
          →
        </button>
      )}
      <AttachButton disabled={isMerged} />
      {voice.showMic && <MicButton disabled={isMerged} />}
      <SendButton disabled={isMerged || composer.uploading > 0} />
    </form>
  );
}
