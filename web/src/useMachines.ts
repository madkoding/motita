// The computers in the sidebar: which one the page is showing, the list the gateway keeps of the
// others, and what can be done with them (rename, connect with a token, add by address, forget).
import { createContext } from "preact";
import { useCallback, useContext, useEffect, useState } from "preact/hooks";
import type { Api } from "./gitApi";
import {
  addMachine,
  connectMachine,
  forgetMachine,
  getMachines,
  renameMachine,
  renameSelf,
  type Machine,
  type MachinesView,
} from "./machinesApi";
import { errorMessage } from "./errors";

// The computer the page shows is remembered in the browser: "" (or nothing) is this one.
export const MACHINE_KEY = "motita.machine";
// How often the list is read again: a computer found or gone shows up without a reload.
export const MACHINES_POLL_MS = 15000;

function readMachine(): string {
  try {
    return localStorage.getItem(MACHINE_KEY) || "";
  } catch {
    return "";
  }
}

function writeMachine(id: string) {
  try {
    localStorage.setItem(MACHINE_KEY, id);
  } catch {
    /* a browser that keeps nothing shows this computer next time */
  }
}

// useMachineChoice is the computer the page shows, remembered across reloads.
export function useMachineChoice(): [string, (id: string) => void] {
  const [id, setId] = useState(readMachine);
  const choose = useCallback((next: string) => {
    writeMachine(next);
    setId(next);
  }, []);
  return [id, choose];
}

// ConnectTarget is what the connect dialog is about: a found computer, or none (an address is
// typed instead).
export interface ConnectTarget {
  machine?: Machine;
}

// useMachines reads the computers from THIS gateway (api is never a proxied one) once the page is
// signed in, and keeps them current.
export function useMachines(
  api: Api,
  enabled: boolean,
  active: string,
  setActive: (id: string) => void,
) {
  const [view, setView] = useState<MachinesView | null>(null);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [connecting, setConnecting] = useState<ConnectTarget | null>(null);
  const [error, setError] = useState("");

  const refresh = useCallback(
    () =>
      getMachines(api).then(
        (v) => setView(v),
        () => undefined,
      ),
    [api],
  );
  useEffect(() => {
    if (!enabled) {
      return;
    }
    void refresh();
    const timer = setInterval(refresh, MACHINES_POLL_MS);
    return () => clearInterval(timer);
  }, [enabled, refresh]);

  // A computer that is no longer connected (forgotten here or elsewhere) cannot be shown: the
  // page goes back to this one.
  useEffect(() => {
    if (
      view &&
      active &&
      !view.peers.some((p) => p.id === active && p.paired)
    ) {
      setActive("");
    }
  }, [view, active, setActive]);

  // apply runs a change and keeps the list it answers; a failure is kept to be shown.
  const apply = useCallback(async (change: Promise<MachinesView>) => {
    try {
      setView(await change);
      setError("");
      return "";
    } catch (e) {
      const msg = errorMessage(e);
      setError(msg);
      return msg;
    }
  }, []);

  // toggle opens or closes a computer's box; picking another computer shows it instead.
  const select = useCallback(
    (id: string) => {
      if (id !== active) {
        setActive(id);
        return;
      }
      setCollapsed((prev) => {
        const next = new Set(prev);
        if (!next.delete(id)) {
          next.add(id);
        }
        return next;
      });
    },
    [active, setActive],
  );

  const connect = useCallback(
    (token: string, address: string) => {
      const m = connecting?.machine;
      const change = m
        ? connectMachine(api, m.id, token)
        : addMachine(api, address, token);
      return apply(change).then((msg) => {
        if (!msg) {
          setConnecting(null);
        }
        return msg;
      });
    },
    [api, apply, connecting],
  );

  return {
    view,
    active,
    error,
    select,
    isOpen: (id: string) => id === active && !collapsed.has(id),
    connecting,
    openConnect: setConnecting,
    closeConnect: useCallback(() => setConnecting(null), []),
    connect,
    renameSelf: (name: string) => apply(renameSelf(api, name)),
    rename: (id: string, alias: string) => apply(renameMachine(api, id, alias)),
    forget: (id: string) => apply(forgetMachine(api, id)),
  };
}

export type Machines = ReturnType<typeof useMachines>;

export const MachinesCtx = createContext<Machines | null>(null);

// useMachinesCtx is the computers, for a component under App.
export function useMachinesCtx(): Machines {
  return useContext(MachinesCtx) as Machines;
}
