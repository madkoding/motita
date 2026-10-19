import { useCallback, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import { scanNotes, type NoteScan } from "./notesApi";
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

type OnOff = "on" | "off";

// NotesScanSettings fills the notes from what motita already holds: a note per project and per
// session. Running it again updates the same notes and leaves what the person wrote in them.
export function NotesScanSettings({ api }: { api: Api }) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState("");
  const [err, setErr] = useState("");
  const [summarize, setSummarize] = useState<OnOff>("off");
  const scan = useCallback(async () => {
    setBusy(true);
    setErr("");
    setDone("");
    try {
      setDone(
        scanSummary(await scanNotes(api, { summarize: summarize === "on" })),
      );
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  }, [api, summarize]);
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
            "Adds a short summary to sessions of six turns or more (at most twenty per scan), explains how each project fits together (at most ten per scan) and documents each folder of the projects' manuals (at most thirty per scan). Each is only asked again when the session, project or folder changed. The text of those sessions and the structure, README and source excerpts of those projects are sent to your model provider and use tokens.",
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
            {t("Scan now")}
          </button>
        </Row>
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
