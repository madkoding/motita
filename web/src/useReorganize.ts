// The reorganization of the memory, as the memory window drives it: start it, follow it while it
// runs, stop it, and undo it from the copy the gateway kept before it began.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { t } from "./i18n";
import { HTTP_CONFLICT, JSON_HEADERS } from "./appConst";
import type { Api } from "./memoryApi";
import type { ReorgStatus } from "./memoryKinds";

const REORG_PATH = "/v1/memory/reorganize";

// REORG_POLL is how often a running reorganization is read again; a test shortens it.
export const REORG_POLL = { ms: 1000 };

export function useReorganize(api: Api, reload: () => Promise<void>) {
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState<ReorgStatus | null>(null);
  const [failed, setFailed] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  // round counts the times polling was stopped or restarted: a read that was in flight when the
  // window closed (or the page went away) finds its round over and does nothing.
  const round = useRef(0);
  const stopPolling = () => {
    round.current++;
    clearTimeout(timer.current);
  };
  useEffect(() => stopPolling, []);

  // read asks how the reorganization goes; while it runs it asks again, and once it ends the list
  // is read again, since entries moved.
  const read = useCallback(async (): Promise<void> => {
    stopPolling();
    const mine = round.current;
    try {
      const res = await api(REORG_PATH);
      let st: ReorgStatus | null = null;
      if (res.ok) {
        st = await res.json();
      }
      if (!st || mine !== round.current) {
        return;
      }
      setStatus(st);
      if (st.running) {
        timer.current = setTimeout(() => void read(), REORG_POLL.ms);
        return;
      }
      if (st.report) {
        await reload();
      }
    } catch {
      if (mine === round.current) {
        setFailed(t("could not read how the reorganization goes"));
      }
    }
  }, [api, reload]);

  // show opens the window on the reorganization, running or last run.
  const show = useCallback(() => {
    setFailed("");
    setStatus(null);
    setOpen(true);
    void read();
  }, [read]);

  const hide = useCallback(() => {
    stopPolling();
    setOpen(false);
  }, []);

  const start = useCallback(async () => {
    setFailed("");
    try {
      const res = await api(REORG_PATH, { method: "POST" });
      if (!res.ok && res.status !== HTTP_CONFLICT) {
        setFailed(t("could not start the reorganization"));
        return;
      }
      await read();
    } catch {
      setFailed(t("could not start the reorganization"));
    }
  }, [api, read]);

  const stop = useCallback(async () => {
    try {
      await api(REORG_PATH, { method: "DELETE" });
    } catch {
      // the next read says how it stands
    }
    await read();
  }, [api, read]);

  // undo puts the memory back as it was before the reorganization.
  const undo = useCallback(async () => {
    setFailed("");
    try {
      const res = await api(REORG_PATH + "/undo", {
        method: "POST",
        headers: JSON_HEADERS,
        body: JSON.stringify({ backup: status?.report?.backup ?? "" }),
      });
      if (!res.ok) {
        setFailed(t("could not undo the reorganization"));
        return;
      }
      setStatus(null);
      await reload();
      setOpen(false);
    } catch {
      setFailed(t("could not undo the reorganization"));
    }
  }, [api, reload, status]);

  return { open, status, failed, show, hide, start, stop, undo };
}
