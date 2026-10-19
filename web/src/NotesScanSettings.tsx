import { useCallback, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import type { Api } from "./gitApi";
import { errorMessage } from "./errors";
import { scanNotes, type NoteScan } from "./notesApi";
import { Row, primaryBtn } from "./SettingsControls";

// scanSummary says what a scan did, in the person's words.
function scanSummary(r: NoteScan): string {
  return tf(
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
}

// NotesScanSettings fills the notes from what motita already holds: a note per project and per
// session. Running it again updates the same notes and leaves what the person wrote in them.
export function NotesScanSettings({ api }: { api: Api }) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState("");
  const [err, setErr] = useState("");
  const scan = useCallback(async () => {
    setBusy(true);
    setErr("");
    setDone("");
    try {
      setDone(scanSummary(await scanNotes(api)));
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  }, [api]);
  return (
    <section class="settings-group" aria-labelledby="settings-notes-scan-h">
      <h3 id="settings-notes-scan-h" class="settings-group-title">
        {t("Second brain")}
      </h3>
      <div class="settings-card">
        <Row
          id="settings-notes-scan"
          label={t("Scan sessions and projects")}
          hint={t(
            "Writes a note for every project and every session you have used, linked to each other. Run it again to refresh them; what you add around the generated block stays.",
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
