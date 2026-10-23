// The TTY drawer as a real terminal: the session's shell on a pseudo-terminal, for the commands a
// typed line cannot serve - one that asks for input, an editor, a REPL.
//
// The gateway carries it over a WebSocket (GET /v1/sessions/{id}/terminal): binary frames are the
// terminal's bytes both ways, text frames its control messages (a resize from here; its end or the
// reason it could not open from there). It holds the session's run slot while it is open, so the
// agent waits for it; it ends with the shell (`exit`), with the close button, or when the session
// changes.
import { useEffect, useRef } from "preact/hooks";
import { t } from "./i18n";
import { proxyPath } from "./machinesApi";
import { SESSIONS_PATH } from "./appConst";
import type { XtermView } from "./xtermView";

/** Where the terminal of session sid is, on machine ("" for this one), for a screen of cols × rows. */
export function terminalUrl(
  machine: string,
  sid: string,
  cols: number,
  rows: number,
): string {
  const scheme = location.protocol === "https:" ? "wss://" : "ws://";
  const path = proxyPath(
    machine,
    SESSIONS_PATH +
      encodeURIComponent(sid) +
      "/terminal?cols=" +
      cols +
      "&rows=" +
      rows,
  );
  return scheme + location.host + path;
}

// A text frame from the gateway: how the shell ended, or why there is none.
interface Control {
  type?: string;
  message?: string;
}

function parseControl(data: string): Control {
  try {
    return JSON.parse(data) as Control;
  } catch {
    return {};
  }
}

/** The interactive terminal. onEnd is called once, with the reason when it ended in a failure. */
export function TermPty({
  machine,
  sid,
  open,
  onEnd,
}: {
  machine: string;
  sid: string;
  open: boolean;
  onEnd: (reason?: string) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<XtermView | null>(null);
  const endRef = useRef(onEnd);
  endRef.current = onEnd;

  useEffect(() => {
    let live = true;
    let ended = false;
    let ws: WebSocket | null = null;
    const end = (reason?: string) => {
      if (!ended) {
        ended = true;
        endRef.current(reason);
      }
    };
    import("./xtermView").then(
      (m) => {
        if (!live) {
          return;
        }
        const view = m.createView(hostRef.current as HTMLDivElement);
        viewRef.current = view;
        const { cols, rows } = view.size();
        const socket = new WebSocket(terminalUrl(machine, sid, cols, rows));
        ws = socket;
        socket.binaryType = "arraybuffer";
        const encoder = new TextEncoder();
        socket.onmessage = (e: MessageEvent) => {
          if (typeof e.data !== "string") {
            view.write(new Uint8Array(e.data as ArrayBuffer));
            return;
          }
          const ctl = parseControl(e.data);
          if (ctl.type === "error") {
            end(ctl.message || t("The terminal could not be opened"));
          } else if (ctl.type === "exit") {
            end();
          } else {
            // Anything else is from a newer gateway: nothing to do with it here.
          }
        };
        socket.onclose = () => end();
        socket.onerror = () => end(t("The terminal could not be opened"));
        view.onData((d) => {
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(encoder.encode(d));
          }
        });
        view.onResize((c, r) => {
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "resize", cols: c, rows: r }));
          }
        });
        view.focus();
      },
      () => end(t("The terminal could not be loaded")),
    );
    return () => {
      live = false;
      ended = true;
      ws?.close();
      viewRef.current?.dispose();
      viewRef.current = null;
    };
  }, [machine, sid]);

  // A drawer opened again finds its screen at the size it was left at: it is refitted and focused.
  useEffect(() => {
    if (open && viewRef.current) {
      viewRef.current.fit();
      viewRef.current.focus();
    }
  }, [open]);

  return <div class="term-pty" ref={hostRef} />;
}
