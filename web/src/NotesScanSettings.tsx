import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import {
  SCAN_STARTED_EVENT,
  scanProgress,
  startScan,
  type NoteScan,
  type ScanOptions,
  type ScanProgress,
} from "./notesApi";
import { Row, Segmented, primaryBtn } from "./SettingsControls";
import { REVIEW_NOTES_EVENT } from "./useReviewNotes";

// scanSummary says what a scan did, in the person's words.
function scanSummary(r: NoteScan): string {
  const done = tf(
    "{projects} projects and {sessions} sessions scanned: {created} notes created, {updated} updated, {unchanged} unchanged, {skipped} skipped.",
    {
      projects: String(r.projects),
      sessions: String(r.sessions),
      created: String(r.created),
      updated: String(r.updated),
      unchanged: String(r.unchanged),
      skipped: String(r.skipped),
    },
  );
  const extra: string[] = [];
  if (r.summarized > 0) {
    extra.push(
      tf("{n} long sessions summarised.", { n: String(r.summarized) }),
    );
  }
  if (r.mapped > 0) {
    extra.push(
      tf(
        "{mapped} projects mapped into {documented} manual notes, {explained} explained by the model.",
        {
          mapped: String(r.mapped),
          documented: String(r.documented),
          explained: String(r.explained),
        },
      ),
    );
  }
  return [done, ...extra].join(" ");
}

// progressText says where a running scan is.
export function progressText(p: ScanProgress): string {
  if (p.phase !== "projects") {
    return t("Writing the notes of your sessions…");
  }
  let line = tf("Mapping {project} ({n} of {total})", {
    project: p.project ?? "",
    n: String(p.done + 1),
    total: String(p.total),
  });
  if (p.chapter_total > 0) {
    line +=
      " · " +
      tf("folder {n} of {total}", {
        n: String(p.chapters + 1),
        total: String(p.chapter_total),
      });
  }
  return line;
}

// progressShare is how much of the scan is done, from 0 to 100: the projects finished and the
// share of the folders of the one being mapped.
export function progressShare(p: ScanProgress): number {
  if (p.total === 0) {
    return 0;
  }
  let part = 0;
  if (p.chapter_total > 0) {
    part = p.chapters / p.chapter_total;
  }
  return Math.round(((p.done + part) / p.total) * 100);
}

// scanPoll is how often a running scan is asked how far it got; the tests shorten it.
export const scanPoll = { ms: 1000 };

type OnOff = "on" | "off";
type Folders = "30" | "100" | "all";

// FOLDER_BUDGET is how many folders each choice lets the model explain; "all" is the gateway's
// own ceiling.
const FOLDER_BUDGET: Record<Folders, number> = {
  "30": 30,
  "100": 100,
  all: 2000,
};

// NotesScanSettings fills the notes from what motita already holds: a note per project and per
// session. Running it again updates the same notes and leaves what the person wrote in them.
export function NotesScanSettings({ api }: { api: Api }) {
  const [progress, setProgress] = useState<ScanProgress | null>(null);
  const [done, setDone] = useState("");
  const [err, setErr] = useState("");
  const [summarize, setSummarize] = useState<OnOff>("off");
  const [folders, setFolders] = useState<Folders>("30");
  const alive = useRef(true);
  const busy = progress !== null;
  // watch follows a scan running in the background until it ends, then says what it did.
  const watch = useCallback(
    async (first: ScanProgress) => {
      let p = first;
      while (p.running && alive.current) {
        setProgress(p);
        await new Promise((r) => setTimeout(r, scanPoll.ms));
        p = await scanProgress(api);
      }
      setProgress(null);
      if (p.error) {
        setErr(p.error);
        return;
      }
      if (p.result) {
        setDone(scanSummary(p.result));
      }
    },
    [api],
  );
  // A scan started before this card opened (or in another tab) is followed too.
  useEffect(() => {
    alive.current = true;
    scanProgress(api)
      .then(watch)
      .catch(() => setProgress(null));
    return () => {
      alive.current = false;
    };
  }, [api, watch]);
  const scan = useCallback(async () => {
    setErr("");
    setDone("");
    try {
      const opts: ScanOptions = {};
      if (summarize === "on") {
        opts.summarize = true;
        opts.modules = FOLDER_BUDGET[folders];
      }
      setProgress({
        running: true,
        done: 0,
        total: 0,
        chapters: 0,
        chapter_total: 0,
      });
      const started = await startScan(api, opts);
      window.dispatchEvent(new Event(SCAN_STARTED_EVENT));
      await watch(started);
    } catch (e) {
      setProgress(null);
      setErr(errorMessage(e));
    }
  }, [api, summarize, folders, watch]);
  const review = useCallback(
    () => window.dispatchEvent(new Event(REVIEW_NOTES_EVENT)),
    [],
  );
  return (
    <section class="settings-group" aria-labelledby="settings-notes-scan-h">
      <h3 id="settings-notes-scan-h" class="settings-group-title">
        {t("Second brain")}
      </h3>
      <div class="settings-card">
        <Row
          id="settings-notes-summary"
          label={t("Use the model to summarise sessions and explain projects")}
          hint={t(
            "Adds a short summary to sessions of six turns or more (at most twenty per scan), explains how each project fits together (at most ten per scan) and documents each folder of the projects' manuals (as many per scan as you choose below). Each is only asked again when the session, project or folder changed. The text of those sessions and the structure, README and source excerpts of those projects are sent to your model provider and use tokens.",
          )}
          stack
        >
          <Segmented<OnOff>
            name="settings-notes-summary"
            labelledBy="settings-notes-summary"
            value={summarize}
            onChange={setSummarize}
            disabled={busy}
            options={[
              { value: "on", label: t("On") },
              { value: "off", label: t("Off") },
            ]}
          />
        </Row>
        <Row
          id="settings-notes-folders"
          label={t("Folders the model explains per scan")}
          hint={t(
            "Each folder is one model call, so more folders take longer and use more tokens. A folder already explained is only asked again when it changed.",
          )}
          stack
        >
          <Segmented<Folders>
            name="settings-notes-folders"
            labelledBy="settings-notes-folders"
            value={folders}
            onChange={setFolders}
            disabled={busy || summarize === "off"}
            options={[
              { value: "30", label: "30" },
              { value: "100", label: "100" },
              { value: "all", label: t("All") },
            ]}
          />
        </Row>
        <Row
          id="settings-notes-scan"
          label={t("Scan sessions and projects")}
          hint={t(
            "Writes a note for every project and every session you have used, linked to each other, and a manual of each project with a note per folder: its files, its API, what it uses and what uses it. Run it again to refresh them; what you add around the generated block stays.",
          )}
          stack
        >
          <button
            type="button"
            class={primaryBtn}
            disabled={busy}
            onClick={scan}
            data-testid="notes-scan"
          >
            {choose(busy, t("Scanning…"), t("Scan now"))}
          </button>
        </Row>
        {progress && (
          <output
            class="settings-card-note block"
            data-testid="notes-scan-progress"
          >
            <span class="block text-xs text-[#c8c8d2]">
              {progressText(progress)}
            </span>
            <span class="block mt-1.5 h-2 rounded-full bg-white/10 overflow-hidden">
              <span
                class="block h-full bg-accent transition-[width] duration-500"
                style={{ width: progressShare(progress) + "%" }}
              />
            </span>
          </output>
        )}
        {done && (
          <output
            class="settings-card-note block text-xs text-[#7a7a8c]"
            data-testid="notes-scan-done"
          >
            {done}
          </output>
        )}
        {done && (
          <Row
            id="settings-notes-review"
            label={t("Review the notes")}
            hint={t(
              "Opens a conversation where the agent checks the notes for contradictions, stale notes, missing sources and broken links. It changes nothing until you agree.",
            )}
            stack
          >
            <button
              type="button"
              class={primaryBtn}
              onClick={review}
              data-testid="notes-review-start"
            >
              {t("Review notes")}
            </button>
          </Row>
        )}
        {err && (
          <p
            class="settings-card-note text-xs text-danger"
            role="alert"
            data-testid="notes-scan-error"
          >
            {err}
          </p>
        )}
      </div>
    </section>
  );
}
