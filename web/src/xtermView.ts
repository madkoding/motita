// The interactive terminal's screen: xterm.js, fitted to its box and dressed in the drawer's CRT
// colours. It is its own module so the emulator is loaded only when a terminal is opened: nobody
// pays for it who only reads the history.
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

export interface XtermView {
  size(): { cols: number; rows: number };
  write(data: Uint8Array): void;
  onData(fn: (data: string) => void): void;
  onResize(fn: (cols: number, rows: number) => void): void;
  fit(): void;
  focus(): void;
  dispose(): void;
}

// The drawer's text colour when the page gives none (the TTY green).
const FALLBACK_RGB = "126, 231, 135";

// cssVar is a custom property of el as the page computed it, or fallback.
function cssVar(el: HTMLElement, name: string, fallback: string): string {
  return getComputedStyle(el).getPropertyValue(name).trim() || fallback;
}

/** Opens a terminal screen in host, sized to fill it and refitted whenever host changes size. */
export function createView(host: HTMLElement): XtermView {
  const ink = "rgb(" + cssVar(host, "--tx-rgb", FALLBACK_RGB) + ")";
  const term = new Terminal({
    allowTransparency: true,
    cursorBlink: true,
    fontFamily: cssVar(host, "--font-mono", "monospace"),
    fontSize: 13,
    scrollback: 5000,
    theme: { background: "rgba(0, 0, 0, 0)", foreground: ink, cursor: ink },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(host);
  fit.fit();
  const observer = new ResizeObserver(() => fit.fit());
  observer.observe(host);
  return {
    size: () => ({ cols: term.cols, rows: term.rows }),
    write: (data) => term.write(data),
    onData: (fn) => {
      term.onData(fn);
    },
    onResize: (fn) => {
      term.onResize(({ cols, rows }) => fn(cols, rows));
    },
    fit: () => fit.fit(),
    focus: () => term.focus(),
    dispose: () => {
      observer.disconnect();
      term.dispose();
    },
  };
}
