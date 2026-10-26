// New versions of Motita: the check the page polls, the toast that offers one, and the upgrade
// itself with its progress bar.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { sseData } from "./appParse";
import { choose } from "./choose";
import type { Api } from "./prApi";
import type { SetToast, UpdateInfo, UpgradeProgressEvent } from "./appTypes";
import {
  HEALTH_POLL_MS,
  RESTART_FIRST_POLL_MS,
  RESTART_WAIT_MS,
  SSE_FRAME_END,
  UPDATE_POLL_MS,
  UPGRADE_DISMISS_KEY,
} from "./appConst";

// reloadWhenBack waits for the gateway to come back from an upgrade and reloads the page to pick
// up the new version. It gives up after a while and calls giveUp, so the page can offer a manual
// reload instead of waiting forever.
function reloadWhenBack(giveUp: () => void) {
  setTimeout(() => {
    // Poll health until the gateway is back.
    const poll = setInterval(async () => {
      try {
        const h = await fetch("/v1/health");
        if (h.ok) {
          clearInterval(poll);
          window.location.reload();
        }
      } catch {
        /* keep polling */
      }
    }, HEALTH_POLL_MS);
    setTimeout(() => {
      clearInterval(poll);
      giveUp();
    }, RESTART_WAIT_MS);
  }, RESTART_FIRST_POLL_MS);
}

// readFrames reads an event stream to its end and hands each complete frame to onFrame, in order.
export async function readFrames(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  onFrame: (frame: string) => void,
): Promise<void> {
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      return;
    }
    buffer += decoder.decode(value, { stream: true });
    let idx = buffer.indexOf(SSE_FRAME_END);
    while (idx !== -1) {
      onFrame(buffer.slice(0, idx));
      buffer = buffer.slice(idx + SSE_FRAME_END.length);
      idx = buffer.indexOf(SSE_FRAME_END);
    }
  }
}

// notDismissed says whether a version's toast may be shown: the person did not close it before.
// A browser whose storage cannot be read shows none.
function notDismissed(version: string): boolean {
  try {
    return localStorage.getItem(UPGRADE_DISMISS_KEY) !== version;
  } catch {
    return false;
  }
}

export function useUpdates(api: Api, setToast: SetToast, authOk: boolean) {
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [showUpgrade, setShowUpgrade] = useState(false);
  const [upgradeProgress, setUpgradeProgress] =
    useState<UpgradeProgressEvent | null>(null);
  const [upgradeBusy, setUpgradeBusy] = useState(false);
  const [upgradeError, setUpgradeError] = useState("");
  // Whether the raw upgrade error text is unfolded. Collapsed by default, for
  // the same reason as the toast: the explanation is the point, the exception
  // string is a footnote for a bug report.
  const [showUpgradeError, setShowUpgradeError] = useState(false);
  // Whether the gateway did not come back in time after an upgrade, so the page did not reload by
  // itself and the modal offers to reload it by hand.
  const [reloadStalled, setReloadStalled] = useState(false);
  // Whether the gateway said it is restarting, so the page will reload by itself once it is back.
  // A run that ends without a restart (already up to date, or a gateway that cannot restart
  // itself) leaves the modal open with its message and a Close button. The ref is what the stream
  // reads, since its callback outlives the render that started it.
  const [awaitingReload, setAwaitingReload] = useState(false);
  const restarting = useRef(false);

  // checkForUpdates polls /v1/update/check. When a new version is found, a
  // toast is shown (unless the user has dismissed this version before) and
  // updateInfo is set so the sidebar can show the "Upgrade Motita" button.
  const checkForUpdates = useCallback(async () => {
    try {
      const res = await api("/v1/update/check");
      if (!res.ok) {
        return;
      }
      const data: UpdateInfo = await res.json();
      setUpdateInfo(data);
      // Unless the user has dismissed this version.
      if (
        data.update_available &&
        data.latest_version &&
        notDismissed(data.latest_version)
      ) {
        setToast({
          message: tf("New version {version} available — click to upgrade", {
            version: data.latest_version,
          }),
          type: "success",
          update: true,
        });
      }
    } catch {
      /* non-fatal */
    }
  }, []);

  // onUpgradeFrame applies one event of the upgrade stream and returns its stage.
  const onUpgradeFrame = (frame: string): string | undefined => {
    const dataLine = sseData(frame);
    if (!dataLine) {
      return undefined;
    }
    try {
      const evt: UpgradeProgressEvent = JSON.parse(dataLine);
      setUpgradeProgress(evt);
      if (evt.stage === "error") {
        setUpgradeError(evt.message || t("upgrade failed"));
        setUpgradeBusy(false);
      } else if (evt.stage === "restarting") {
        restarting.current = true;
        setAwaitingReload(true);
      } else if (evt.stage === "done" && restarting.current) {
        // The modal stays locked with a spinner until the gateway is back and the page reloads
        // by itself; only if it never comes back is it unlocked.
        reloadWhenBack(() => {
          setUpgradeBusy(false);
          setReloadStalled(true);
        });
      } else if (evt.stage === "done") {
        setUpgradeBusy(false);
      } else {
        // A stage on the way: the progress bar above already shows it.
      }
      return evt.stage;
    } catch {
      /* ignore parse error */
      return undefined;
    }
  };

  // runUpgrade starts the upgrade stream and updates the progress bar.
  const runUpgrade = useCallback(async () => {
    setUpgradeBusy(true);
    setUpgradeError("");
    setShowUpgradeError(false);
    setReloadStalled(false);
    setAwaitingReload(false);
    restarting.current = false;
    setUpgradeProgress({
      stage: "starting",
      percent: 0,
      message: t("Starting upgrade…"),
    });
    // Once the upgrade is done the gateway restarts, and the stream may break as it goes down:
    // that is the restart, not a failure.
    let finished = false;
    try {
      const res = await api("/v1/update/run", { method: "POST" });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        setUpgradeError(err.error || t("could not start the upgrade"));
        setUpgradeBusy(false);
        return;
      }
      await readFrames(res.body!.getReader(), (frame) => {
        if (onUpgradeFrame(frame) === "done" && restarting.current) {
          finished = true;
        }
      });
    } catch (e) {
      if (finished) {
        return;
      }
      setUpgradeError(String(e));
      setUpgradeBusy(false);
    }
  }, []);

  // dismissUpgradeToast hides the "new version" toast and remembers the
  // dismissal so it does not reappear for the same version.
  const dismissUpgradeToast = useCallback(() => {
    if (updateInfo?.latest_version) {
      try {
        localStorage.setItem(UPGRADE_DISMISS_KEY, updateInfo.latest_version);
      } catch {
        /* ignore */
      }
    }
    setToast(null);
  }, [updateInfo]);

  // Poll for updates: once on startup (after auth succeeds) and then every
  // 10 minutes. The backend's own hourly check is what populates the cache,
  // so this poll is cheap (it reads the cache, not GitHub).
  useEffect(() => {
    if (!authOk) {
      return undefined;
    }
    checkForUpdates();
    const interval = setInterval(checkForUpdates, UPDATE_POLL_MS);
    return () => clearInterval(interval);
  }, [authOk, checkForUpdates]);

  // Opening the modal takes the "new version" toast down: it offers what the modal now shows, and
  // it was drawn over the modal's own title.
  const openUpgrade = useCallback(() => {
    setShowUpgrade(true);
    setToast((prev) => choose(prev?.update === true, null, prev));
  }, []);
  // closeUpgradeIfIdle closes the modal, unless an upgrade is running: it cannot be walked away from.
  const closeUpgradeIfIdle = useCallback(() => {
    if (!upgradeBusy) {
      setShowUpgrade(false);
    }
  }, [upgradeBusy]);
  const cancelUpgrade = useCallback(() => setShowUpgrade(false), []);
  // finishUpgrade closes the modal after an upgrade ended, and forgets how it went.
  const finishUpgrade = useCallback(() => {
    setShowUpgrade(false);
    setUpgradeProgress(null);
    setUpgradeError("");
    setShowUpgradeError(false);
  }, []);
  const toggleUpgradeError = useCallback(
    () => setShowUpgradeError((v) => !v),
    [],
  );
  const reloadPage = useCallback(() => window.location.reload(), []);

  return {
    updateInfo,
    showUpgrade,
    upgradeProgress,
    upgradeBusy,
    upgradeError,
    showUpgradeError,
    reloadStalled,
    awaitingReload,
    reloadPage,
    runUpgrade,
    dismissUpgradeToast,
    openUpgrade,
    closeUpgradeIfIdle,
    cancelUpgrade,
    finishUpgrade,
    toggleUpgradeError,
  };
}

export type Updates = ReturnType<typeof useUpdates>;
