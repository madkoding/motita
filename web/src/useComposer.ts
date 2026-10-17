// The composer: what is typed, the slash commands shown as tags above it, the autocomplete popup,
// and what Enter and the send button do with it.
import { useState } from "preact/hooks";
import { choose } from "./choose";
import { shellCommandOf } from "./appParse";
import type { RunState } from "./useRunState";
import type { Drawers } from "./useDrawers";
import type { Stream } from "./useStream";
import type { ActiveTag, SlashCommand } from "./appTypes";
import { SLASH_POPUP_MAX, TASK_INPUT_ID } from "./appConst";

// tagSeq numbers the tags as they are added, so removing one never renames the others.
let tagSeq = 0;

// tagOf makes a command a tag of the composer.
function tagOf(c: { name: string; group: string }): ActiveTag {
  tagSeq += 1;
  return { name: c.name, group: c.group, key: tagSeq };
}

// composedText is what is sent: the tags first, then what was typed after them.
function composedText(input: string, tags: ActiveTag[]): string {
  const argText = input.trim();
  if (tags.length === 0) {
    return argText;
  }
  const tagText = tags.map((tag) => tag.name).join(" ");
  return choose(argText, tagText + " " + argText, tagText);
}

// namedBy says whether a command is the one a word names, by its name or by an alias.
function namedBy(c: SlashCommand, word: string): boolean {
  return (
    c.name.toLowerCase() === word ||
    (c.aliases || []).some((a) => a.toLowerCase() === word)
  );
}

// startsWith says whether a command's name or one of its aliases begins with what was typed.
function startsWith(c: SlashCommand, query: string): boolean {
  return (
    c.name.toLowerCase().startsWith(query) ||
    (c.aliases || []).some((a) => a.toLowerCase().startsWith(query))
  );
}

// isAccept says whether a key takes the highlighted command of the popup.
function isAccept(e: KeyboardEvent): boolean {
  const plain = !e.shiftKey && !e.ctrlKey && !e.metaKey;
  return e.key === "Tab" || (e.key === "Enter" && plain && !e.altKey);
}

// keepsLine says whether an Enter leaves the line alone instead of sending it: Shift, Ctrl or
// Meta insert a newline, and Alt does too unless a run is in flight (then it queues the message).
function keepsLine(e: KeyboardEvent, running: boolean): boolean {
  if (e.shiftKey || e.ctrlKey || e.metaKey) {
    return true;
  }
  return e.altKey && !running;
}

interface Deps {
  rs: RunState;
  drawers: Drawers;
  stream: Stream;
  runShellLine: (command: string) => Promise<void>;
  // nextSuggestion is what the empty field hints at after a turn; "" when there is none.
  nextSuggestion: string;
}

export function useComposer({
  rs,
  drawers,
  stream,
  runShellLine,
  nextSuggestion,
}: Deps) {
  const { runningRef } = rs;
  const [input, setInput] = useState("");
  // Slash commands loaded from the backend, and the autocomplete popup state.
  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>([]);
  const [slashPopup, setSlashPopup] = useState<{
    items: SlashCommand[];
    index: number;
  } | null>(null);
  // Active slash command tags: when set, the composer shows them as pills
  // above the textarea. Multiple tags can be active at once.
  const [activeTags, setActiveTags] = useState<ActiveTag[]>([]);

  // plan and task are mutually exclusive — if one is already an active tag,
  // the other is blocked from being added.
  const isCommandBlocked = (cmdName: string) => {
    if (cmdName === "/plan") {
      return activeTags.some((tag) => tag.name === "/task");
    }
    if (cmdName === "/task") {
      return activeTags.some((tag) => tag.name === "/plan");
    }
    return false;
  };

  // Choosing a slash command from the popup (by click, or Enter/Space on the row) adds it as a tag.
  const pickSlash = (c: { name: string; group: string }) => {
    if (isCommandBlocked(c.name)) {
      return;
    }
    setActiveTags((prev) => [...prev, tagOf(c)]);
    setInput("");
    setSlashPopup(null);
    document.getElementById(TASK_INPUT_ID)?.focus();
  };

  // removeTag takes one tag off the composer.
  const removeTag = (key: number) =>
    setActiveTags((prev) => prev.filter((tag) => tag.key !== key));

  // onInput keeps the text and drives the autocomplete: when the input starts with / and has no
  // space, the commands are filtered. When it gets a space, a first word that is a valid command
  // is turned into a tag.
  const onInput = (val: string) => {
    setInput(val);
    if (!val.startsWith("/")) {
      setSlashPopup(null);
      return;
    }
    if (!val.includes(" ")) {
      const query = val.toLowerCase();
      const matches = slashCommands.filter(
        (c) => !isCommandBlocked(c.name) && startsWith(c, query),
      );
      setSlashPopup(
        choose(
          matches.length > 0,
          { items: matches.slice(0, SLASH_POPUP_MAX), index: 0 },
          null,
        ),
      );
      return;
    }
    // Check if the first word is a valid command.
    const firstWord = val.split(" ")[0].toLowerCase();
    const match = slashCommands.find((c) => namedBy(c, firstWord));
    if (match && !isCommandBlocked(match.name)) {
      setActiveTags((prev) => [...prev, tagOf(match)]);
      setInput(val.substring(firstWord.length + 1).replace(/^\s+/, ""));
    }
    setSlashPopup(null);
  };

  // clear empties the composer once what it held has been dealt with.
  const clear = () => {
    setInput("");
    setActiveTags([]);
  };

  // send does what the composer holds: /agents opens the agents drawer, a shell line runs in the
  // shell, a message to a run in flight interrupts it (or waits, with `interrupt` off), and
  // anything else starts a run.
  const send = (interrupt: boolean) => {
    const text = composedText(input, activeTags);
    if (text === "/agents") {
      drawers.showAgents();
      clear();
      return;
    }
    if (!text) {
      return;
    }
    const shellLine = shellCommandOf(text);
    if (shellLine) {
      clear();
      void runShellLine(shellLine);
      return;
    }
    if (runningRef.current) {
      clear();
      stream.sendWhileRunning(text, interrupt);
      return;
    }
    // Remove placeholder messages (.kind) before the first real turn.
    rs.setMessages((prev) => prev.filter((m) => m.kind !== "kind"));
    clear();
    stream.submit(text);
  };

  // handleSubmit is called when the form is submitted.
  const handleSubmit = (e: Event) => {
    e.preventDefault();
    send(true);
  };

  // acceptSlash takes the highlighted command of the popup.
  const acceptSlash = (items: SlashCommand[], index: number) => {
    const selected = items[index];
    if (selected?.name === "/agents") {
      // A view of this page, not a request to the agent: it opens the agents drawer, as the
      // TUI's /agents opens its panel.
      drawers.showAgents();
      setInput("");
      setSlashPopup(null);
      return;
    }
    if (selected && !isCommandBlocked(selected.name)) {
      setActiveTags((prev) => [...prev, tagOf(selected)]);
      setInput("");
      setSlashPopup(null);
    }
  };

  // slashKey handles the keys the popup answers, and says whether it did.
  const slashKey = (e: KeyboardEvent): boolean => {
    if (!slashPopup) {
      return false;
    }
    const n = slashPopup.items.length;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSlashPopup({ ...slashPopup, index: (slashPopup.index + 1) % n });
      return true;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setSlashPopup({ ...slashPopup, index: (slashPopup.index - 1 + n) % n });
      return true;
    }
    if (isAccept(e)) {
      e.preventDefault();
      acceptSlash(slashPopup.items, slashPopup.index);
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      setSlashPopup(null);
      return true;
    }
    return false;
  };

  // acceptSuggestion: ArrowRight on an empty field takes the hint as the text, ready to send or edit.
  const acceptSuggestion = (e: KeyboardEvent) => {
    const plain = !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey;
    const canTake = input === "" && activeTags.length === 0 && nextSuggestion;
    if (e.key !== "ArrowRight" || !plain || !canTake) {
      return false;
    }
    e.preventDefault();
    setInput(nextSuggestion);
    return true;
  };

  // handleKeydown: Enter sends on desktop (no modifier), Shift+Enter inserts newline.
  // On touch devices Enter always inserts a newline — a physical keyboard is
  // the signal that single-line Enter-to-send is expected.
  // When the slash command popup is open, ArrowUp/Down navigate and Tab/Enter
  // accept the selection.
  const handleKeydown = (e: KeyboardEvent) => {
    if (slashKey(e)) {
      return;
    }
    if (e.key === "Escape" && runningRef.current) {
      e.preventDefault();
      stream.stopRun();
      return;
    }
    if (acceptSuggestion(e)) {
      return;
    }
    if (e.key !== "Enter") {
      return;
    }
    // Coarse pointer = touch device; skip Enter-to-send there.
    if (window.matchMedia("(pointer: coarse)").matches) {
      return;
    }
    if (keepsLine(e, runningRef.current)) {
      return;
    }
    e.preventDefault();
    // Enter interrupts with the message; Alt+Enter leaves the run alone and queues it.
    send(!e.altKey);
  };

  // shellMode: what is typed is a shell command (`!ls` or `/shell ls`), so the send button says so.
  const shellMode = activeTags.length === 0 && /^\s*(!|\/shell\s)/.test(input);

  return {
    input,
    setInput,
    slashCommands,
    setSlashCommands,
    slashPopup,
    setSlashPopup,
    activeTags,
    pickSlash,
    removeTag,
    onInput,
    handleSubmit,
    handleKeydown,
    // sendQueued sends without interrupting a run in flight: for a message nobody pressed send on.
    sendQueued: () => send(false),
    shellMode,
    nextSuggestion,
  };
}

export type Composer = ReturnType<typeof useComposer>;
