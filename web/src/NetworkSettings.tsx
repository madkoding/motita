import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  type ChosenMode,
  type NetworkMode,
  type NetworkView,
  getNetwork,
  setNetworkMode,
  setTailscale,
  restartGateway,
} from "./networkApi";
import { copyText } from "./clipboard";
import {
  Row,
  Segmented,
  dangerBtn,
  primaryBtn,
  smallBtn,
} from "./SettingsControls";

// The page Tailscale is installed from, opened in a new tab.
const TAILSCALE_DOWNLOAD = "https://tailscale.com/download";

// How the page waits for the gateway after asking it to restart: the first health check comes
// after firstPollMs (the process restarts about half a second after it answers), then one every
// pollMs, for up to waitMs. health and reload are here so the tests can stand in for them.
export const restartTiming = {
  firstPollMs: 1500,
  pollMs: 1000,
  waitMs: 30000,
  health: (): Promise<Response> =>
    fetch("/v1/health", { credentials: "same-origin", cache: "no-store" }),
  reload: (): void => window.location.reload(),
};

// waitForGateway polls the health endpoint until the restarted gateway answers, then reloads the
// page. It calls onGone when the gateway does not come back in time (the new setting may no longer
// reach this device). The returned function stops the polling.
function waitForGateway(onGone: () => void): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = Date.now() + restartTiming.waitMs;
  // poll only runs from the pending timer, which stop clears: it starts only while not stopped.
  const poll = async () => {
    let ok = false;
    try {
      ok = (await restartTiming.health()).ok;
    } catch {
      /* not back yet */
    }
    if (stopped) {
      return;
    }
    if (ok) {
      restartTiming.reload();
      return;
    }
    if (Date.now() >= deadline) {
      onGone();
      return;
    }
    timer = setTimeout(() => void poll(), restartTiming.pollMs);
  };
  timer = setTimeout(() => void poll(), restartTiming.firstPollMs);
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}

// The command that lets the person's user run tailscale serve without sudo; the gateway's error
// names it when tailscale refuses.
const OPERATOR_COMMAND = "sudo tailscale set --operator=$USER";

// CommandCopy is a copy button for a command shown next to it.
function CommandCopy({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      class={smallBtn}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true);
          setTimeout(() => setDone(false), 1800);
        }
      }}
    >
      {choose(done, t("Copied"), t("Copy"))}
    </button>
  );
}

// NetworkErrorText is an error of the network section. When tailscale refused for lack of
// permission, the command that fixes it is set apart, with a copy button and what to do next.
function NetworkErrorText({ text }: { text: string }) {
  if (!text.includes("--operator")) {
    return <>{text}</>;
  }
  const at = text.indexOf("sudo tailscale set");
  const before = choose(at >= 0, text.slice(0, at).trimEnd(), text);
  return (
    <>
      {before}{" "}
      <span class="inline-flex flex-wrap items-center gap-2 mt-1 text-[#e8e8ea]">
        <code class="break-all" data-testid="operator-command">
          {OPERATOR_COMMAND}
        </code>
        <CommandCopy text={OPERATOR_COMMAND} />
      </span>
      <span class="block mt-1 text-[#7a7a8c]">
        {t("Then turn the switch on again.")}
      </span>
    </>
  );
}

// restartLines is what the confirmation spells out before the gateway restarts itself.
function restartLines(): string[] {
  return [
    t("The gateway restarts to apply the saved setting."),
    t(
      "Running work is interrupted for a moment; saved sessions resume after the restart.",
    ),
    t(
      "If the new setting no longer reaches this device (for example, you chose “This machine only” and opened this page from another device), this page will stop working: open it from the gateway's machine.",
    ),
  ];
}

// modeLabel names a network mode the way the choices do.
function modeLabel(m: NetworkMode): string {
  if (m === "local") {
    return t("This machine only");
  }
  if (m === "lan") {
    return t("Local network");
  }
  if (m === "all") {
    return t("Everywhere");
  }
  return t("Custom address");
}

// modeHint says who can connect with a mode, and that this machine alone is the safest.
function modeHint(m: NetworkMode, listen: string): string {
  if (m === "local") {
    return t("Safest: only this machine can open Motita (127.0.0.1).");
  }
  if (m === "lan") {
    return t(
      "Listens on every interface, but only private (local network) addresses may connect. This machine only is the safest choice.",
    );
  }
  if (m === "all") {
    return t(
      "Listens on 0.0.0.0: any address that can reach this machine may connect. This machine only is the safest choice.",
    );
  }
  return tf(
    "Set by hand in the configuration: {listen}. Choosing an option replaces it.",
    { listen },
  );
}

// modeConsequences is what the confirmation spells out before a mode is saved: who gains or
// loses access, and that the token alone guards the gateway once other machines reach it.
function modeConsequences(m: ChosenMode): string[] {
  if (m === "local") {
    return [
      t(
        "Other devices will lose access: only this machine will be able to open Motita.",
      ),
    ];
  }
  const who = choose(
    m === "lan",
    t("Devices on your local network will be able to reach the gateway."),
    t(
      "Anyone who can reach this machine over the network will be able to reach the gateway.",
    ),
  );
  return [
    who,
    t(
      "The gateway speaks plain HTTP: the token travels unencrypted over the network.",
    ),
    t(
      "The token is the only thing protecting a program that runs commands on this machine.",
    ),
  ];
}

// safeHttps is the address when it is an https link, "" otherwise: the page never links to
// anything else the gateway hands it.
function safeHttps(url: string | undefined): string {
  if (url && /^https:\/\/[^\s"'<>]+$/.test(url)) {
    return url;
  }
  return "";
}

// InlineConfirm asks before a sensitive change, inside the card: what it implies, then Cancel and
// Confirm. Cancel takes the focus, so Enter right away never applies the change. The Security tab
// asks with it too, under its own testid.
export function InlineConfirm({
  id,
  title,
  lines,
  confirmLabel,
  danger,
  busy,
  onConfirm,
  onCancel,
  testid = "network-confirm",
}: {
  id: string;
  title: string;
  lines: string[];
  confirmLabel: string;
  danger: boolean;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  testid?: string;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    cancelRef.current?.focus();
  }, []);
  return (
    <fieldset
      aria-labelledby={id}
      class="settings-confirm min-w-0"
      data-testid={testid}
    >
      <div id={id} class="text-sm font-semibold text-[#e8e8ea]">
        {title}
      </div>
      <ul class="settings-confirm-lines">
        {lines.map((l) => (
          <li key={l}>{l}</li>
        ))}
      </ul>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          ref={cancelRef}
          class={smallBtn}
          disabled={busy}
          onClick={onCancel}
        >
          {t("Cancel")}
        </button>
        <button
          type="button"
          class={choose(danger, dangerBtn, primaryBtn)}
          disabled={busy}
          onClick={onConfirm}
        >
          {confirmLabel}
        </button>
      </div>
    </fieldset>
  );
}

type NetworkPending =
  | { kind: "mode"; mode: ChosenMode }
  | { kind: "tailscale" }
  | { kind: "restart" };

// TailscaleRow is Tailscale's state: how to get it, that it is not connected, or the switch that
// publishes the gateway inside the tailnet with `tailscale serve`.
function TailscaleRow({
  view,
  busy,
  asking,
  onAsk,
  onCancel,
  onOff,
}: {
  view: NetworkView;
  busy: boolean;
  asking: boolean;
  onAsk: () => void;
  onCancel: () => void;
  onOff: () => void;
}) {
  const ts = view.tailscale;
  if (!ts?.installed) {
    return (
      <Row
        id="settings-tailscale"
        label="Tailscale"
        hint={t(
          "Not installed. Tailscale gives you private https access from your own devices, without opening the gateway to the network.",
        )}
      >
        <a
          href={TAILSCALE_DOWNLOAD}
          target="_blank"
          rel="noopener noreferrer"
          class={smallBtn + " inline-flex items-center"}
        >
          {t("Get Tailscale")} ↗
        </a>
      </Row>
    );
  }
  if (!ts.running) {
    return (
      <Row
        id="settings-tailscale"
        label="Tailscale"
        hint={choose(
          ts.state,
          tf("Tailscale is installed but not connected ({state}).", {
            state: ts.state,
          }),
          t("Tailscale is installed but not connected."),
        )}
      >
        {null}
      </Row>
    );
  }
  const url = safeHttps(ts.url);
  let hint: ComponentChildren = t(
    "Only the devices in your tailnet can open it, with https; not the internet.",
  );
  if (ts.serving && url) {
    hint = (
      <>
        {t("Published at")}{" "}
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          class="text-accent hover:underline break-all"
          data-testid="tailscale-url"
        >
          {url}
        </a>
      </>
    );
  }
  return (
    <Row
      id="settings-tailscale"
      label={t("Publish in my tailnet (https)")}
      hint={hint}
    >
      <label class="settings-switch-hit">
        <input
          type="checkbox"
          role="switch"
          class="settings-switch"
          aria-labelledby="settings-tailscale"
          disabled={busy}
          checked={ts.serving || asking}
          onChange={(e) => {
            const on = (e.target as HTMLInputElement).checked;
            if (on) {
              onAsk();
            } else if (asking) {
              onCancel();
            } else {
              onOff();
            }
          }}
        />
      </label>
    </Row>
  );
}

// Restarting is where a restart the page asked for stands: waiting for the gateway, or it did not
// come back here in time.
type Restarting = "" | "waiting" | "gone";

// useNetwork holds the network section's state: what the gateway reported, the change waiting for
// a confirmation, and the restart under way, with the actions that move them.
function useNetwork(api: Api) {
  const [view, setView] = useState<NetworkView | null | undefined>(undefined);
  const [loadErr, setLoadErr] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<NetworkPending | null>(null);
  const [restarting, setRestarting] = useState<Restarting>("");
  const stopWaiting = useRef<(() => void) | null>(null);

  useEffect(() => () => stopWaiting.current?.(), []);

  useEffect(() => {
    let live = true;
    getNetwork(api)
      .then((v) => {
        if (live) {
          setView(v);
        }
      })
      .catch((e) => {
        if (live) {
          setLoadErr(errorMessage(e));
        }
      });
    return () => {
      live = false;
    };
  }, [api]);

  const run = useCallback(async (req: () => Promise<NetworkView>) => {
    setBusy(true);
    setErr("");
    try {
      setView(await req());
    } catch (e) {
      setErr(errorMessage(e));
    }
    setPending(null);
    setBusy(false);
  }, []);

  const confirmRestart = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      setView(await restartGateway(api));
    } catch (e) {
      setErr(errorMessage(e));
      setPending(null);
      setBusy(false);
      return;
    }
    setPending(null);
    setRestarting("waiting");
    stopWaiting.current = waitForGateway(() => {
      stopWaiting.current = null;
      setRestarting("gone");
      setBusy(false);
    });
  }, [api]);

  const activeMode = view?.mode;
  const pickMode = useCallback(
    (m: NetworkMode) => {
      if (m === "custom" || m === activeMode) {
        setPending(null);
        return;
      }
      setErr("");
      setPending({ kind: "mode", mode: m });
    },
    [activeMode],
  );
  const confirmMode = useCallback(() => {
    if (pending?.kind === "mode") {
      void run(() => setNetworkMode(api, pending.mode));
    }
  }, [api, pending, run]);
  const cancel = useCallback(() => setPending(null), []);
  const askRestart = useCallback(() => {
    setErr("");
    setRestarting("");
    setPending({ kind: "restart" });
  }, []);
  const askTailscale = useCallback(() => {
    setErr("");
    setPending({ kind: "tailscale" });
  }, []);
  const publishTailscale = useCallback(
    () => void run(() => setTailscale(api, true)),
    [api, run],
  );
  const tailscaleOff = useCallback(
    () => void run(() => setTailscale(api, false)),
    [api, run],
  );

  return {
    view,
    loadErr,
    err,
    busy,
    pending,
    restarting,
    pickMode,
    confirmMode,
    cancel,
    askRestart,
    confirmRestart,
    askTailscale,
    publishTailscale,
    tailscaleOff,
  };
}

type NetworkState = ReturnType<typeof useNetwork>;

// ModeRow is who can connect: the three choices, and what the one shown implies.
function ModeRow({
  view,
  shown,
  busy,
  onPick,
}: {
  view: NetworkView;
  shown: NetworkMode;
  busy: boolean;
  onPick: (m: NetworkMode) => void;
}) {
  return (
    <Row
      id="settings-network"
      label={t("Who can connect")}
      stack
      hint={
        modeHint(shown, view.listen) +
        choose(
          view.can_save,
          "",
          " " +
            t(
              "The gateway runs on its built-in defaults, without a configuration file, so this cannot be changed here.",
            ),
        )
      }
    >
      <Segmented<NetworkMode>
        name="settings-network"
        labelledBy="settings-network"
        value={shown}
        onChange={onPick}
        disabled={!view.can_save || busy}
        options={[
          { value: "local", label: t("This machine only") },
          { value: "lan", label: t("Local network") },
          { value: "all", label: t("Everywhere") },
        ]}
      />
    </Row>
  );
}

interface ConfirmProps {
  pending: NetworkPending | null;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

// ModeConfirm asks before a new mode is saved, while one is waiting.
function ModeConfirm({ pending, busy, onConfirm, onCancel }: ConfirmProps) {
  if (pending?.kind !== "mode") {
    return null;
  }
  return (
    <InlineConfirm
      id="settings-network-confirm"
      title={tf("Switch to “{mode}”?", {
        mode: modeLabel(pending.mode),
      })}
      lines={[
        ...modeConsequences(pending.mode),
        t(
          "The change is saved to the configuration and applies when the gateway restarts: run motita gateway stop, then motita gateway start.",
        ),
      ]}
      confirmLabel={t("Confirm")}
      danger={pending.mode !== "local"}
      busy={busy}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}

// RestartNotice says a saved setting waits for a restart, and offers one where the gateway can.
function RestartNotice({
  view,
  busy,
  pending,
  restarting,
  onAsk,
}: {
  view: NetworkView;
  busy: boolean;
  pending: NetworkPending | null;
  restarting: Restarting;
  onAsk: () => void;
}) {
  if (!view.restart_needed) {
    return null;
  }
  const running = {
    mode: modeLabel(view.active_mode),
    listen: view.active_listen,
  };
  return (
    <output class="settings-row" data-testid="network-restart">
      <div class="settings-row-text">
        <div class="text-sm text-warning">
          {t("Saved. Applies when the gateway restarts")}
        </div>
        <div class="text-xs text-[#7a7a8c] mt-0.5">
          {choose(
            view.can_restart,
            tf("Running now: {mode} ({listen}).", running),
            tf(
              "Running now: {mode} ({listen}). Restart it with motita gateway stop, then motita gateway start.",
              running,
            ),
          )}
        </div>
      </div>
      {view.can_restart && (
        <div class="settings-row-control">
          <button
            type="button"
            class={smallBtn}
            disabled={busy || restarting === "waiting"}
            aria-expanded={pending?.kind === "restart"}
            data-testid="network-restart-now"
            onClick={onAsk}
          >
            {t("Restart now")}
          </button>
        </div>
      )}
    </output>
  );
}

// RestartConfirm asks before the gateway restarts itself.
function RestartConfirm({ pending, busy, onConfirm, onCancel }: ConfirmProps) {
  if (pending?.kind !== "restart") {
    return null;
  }
  return (
    <InlineConfirm
      id="settings-restart-confirm"
      title={t("Restart the gateway now?")}
      lines={restartLines()}
      confirmLabel={t("Restart")}
      danger={false}
      busy={busy}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}

// RestartingNote is the restart under way, or that the gateway did not come back here.
function RestartingNote({ restarting }: { restarting: Restarting }) {
  if (!restarting) {
    return null;
  }
  const gone = restarting === "gone";
  return (
    <output
      class={
        "block settings-card-note text-xs " +
        choose(gone, "text-danger", "text-warning")
      }
      data-testid="network-restarting"
    >
      {choose(
        gone,
        t(
          "The gateway did not come back here. If you changed who can reach it, open it from an address that the new setting allows.",
        ),
        t("Restarting the gateway…"),
      )}
    </output>
  );
}

// TailscaleConfirm asks before the gateway is published in the tailnet.
function TailscaleConfirm({
  pending,
  busy,
  onConfirm,
  onCancel,
}: ConfirmProps) {
  if (pending?.kind !== "tailscale") {
    return null;
  }
  return (
    <InlineConfirm
      id="settings-tailscale-confirm"
      title={t("Publish in your tailnet?")}
      lines={[
        t(
          "This runs tailscale serve: the gateway is published with https only to the devices in your tailnet, not to the internet.",
        ),
        t("Anyone on those devices still needs the token to get in."),
      ]}
      confirmLabel={t("Publish")}
      danger={false}
      busy={busy}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}

// TailscaleError is what tailscale itself reported, if anything.
function TailscaleError({ view }: { view: NetworkView }) {
  const error = view.tailscale?.error;
  if (!error) {
    return null;
  }
  return (
    <p class="settings-card-note text-xs text-[#7a7a8c]">
      <NetworkErrorText text={tf("Tailscale: {error}", { error })} />
    </p>
  );
}

// NetworkControls are the rows of the section once the gateway answered.
function NetworkControls({
  view,
  net,
}: {
  view: NetworkView;
  net: NetworkState;
}) {
  const { pending, busy } = net;
  let shown: NetworkMode = view.mode ?? "custom";
  if (pending?.kind === "mode") {
    shown = pending.mode;
  }
  return (
    <>
      <ModeRow view={view} shown={shown} busy={busy} onPick={net.pickMode} />
      <ModeConfirm
        pending={pending}
        busy={busy}
        onConfirm={net.confirmMode}
        onCancel={net.cancel}
      />
      <RestartNotice
        view={view}
        busy={busy}
        pending={pending}
        restarting={net.restarting}
        onAsk={net.askRestart}
      />
      <RestartConfirm
        pending={pending}
        busy={busy}
        onConfirm={net.confirmRestart}
        onCancel={net.cancel}
      />
      <RestartingNote restarting={net.restarting} />
      <TailscaleRow
        view={view}
        busy={busy}
        asking={pending?.kind === "tailscale"}
        onAsk={net.askTailscale}
        onCancel={net.cancel}
        onOff={net.tailscaleOff}
      />
      <TailscaleConfirm
        pending={pending}
        busy={busy}
        onConfirm={net.publishTailscale}
        onCancel={net.cancel}
      />
      <TailscaleError view={view} />
    </>
  );
}

// NetworkSettings is how the gateway is exposed: the address it listens on (saved in the
// configuration, applied at the next start) and Tailscale. Nothing changes without a confirmation
// that says what it implies. The section stays hidden on a gateway without the network API.
export function NetworkSettings({ api }: { api: Api }) {
  const net = useNetwork(api);
  const { view, loadErr, err } = net;
  if (view === null || (view === undefined && !loadErr)) {
    return null;
  }
  return (
    <section class="settings-group" aria-labelledby="settings-network-h">
      <h3 id="settings-network-h" class="settings-group-title">
        {t("Network")}
      </h3>
      <div class="settings-card" data-testid="network-setting">
        {loadErr && (
          <p class="settings-card-note text-xs text-danger" role="alert">
            {loadErr}
          </p>
        )}
        {view && <NetworkControls view={view} net={net} />}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="network-error"
          >
            <NetworkErrorText text={err} />
          </p>
        )}
      </div>
    </section>
  );
}
