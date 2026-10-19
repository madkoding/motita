// ScanBar follows a notes scan running in the background from the sidebar, so a rescan from the
// project menu shows how far it got without opening Settings. Nothing is drawn while none runs.
import { useEffect, useState } from "preact/hooks";
import { t } from "./i18n";
import type { Api } from "./gitApi";
import {
  SCAN_STARTED_EVENT,
  scanProgress,
  type ScanProgress,
} from "./notesApi";
import { progressShare, progressText, scanPoll } from "./NotesScanSettings";

// askProgress is the scan's progress, or null when the gateway cannot say (notes off, offline).
async function askProgress(api: Api): Promise<ScanProgress | null> {
  try {
    return await scanProgress(api);
  } catch {
    return null;
  }
}

export function ScanBar({ api }: { api: Api }) {
  const [progress, setProgress] = useState<ScanProgress | null>(null);
  useEffect(() => {
    let alive = true;
    let asking = false;
    let missed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // follow asks once, and again after a pause while the scan runs. A start announced while it
    // is asking is asked about again once that answer is in: the answer may predate the start.
    const follow = async () => {
      if (asking) {
        missed = true;
        return;
      }
      clearTimeout(timer);
      asking = true;
      const p = await askProgress(api);
      asking = false;
      if (!alive) {
        return;
      }
      if (missed) {
        missed = false;
        void follow();
        return;
      }
      if (!p?.running) {
        setProgress(null);
        return;
      }
      setProgress(p);
      timer = setTimeout(follow, scanPoll.ms);
    };
    // A scan already running when the page opens is followed too.
    void follow();
    window.addEventListener(SCAN_STARTED_EVENT, follow);
    return () => {
      alive = false;
      clearTimeout(timer);
      window.removeEventListener(SCAN_STARTED_EVENT, follow);
    };
  }, [api]);
  if (!progress) {
    return null;
  }
  return (
    <output
      class="block px-6 py-2 border-t border-white/5"
      data-testid="sidebar-scan"
      aria-live="polite"
    >
      <span class="block text-xs text-[#c8c8d2] truncate">
        {t("Scanning the notes")}
        {" · "}
        {progressText(progress)}
      </span>
      <span class="block mt-1 h-1 rounded-full bg-white/10 overflow-hidden">
        <span
          class="block h-full bg-accent transition-[width] duration-500"
          style={{ width: progressShare(progress) + "%" }}
        />
      </span>
    </output>
  );
}
