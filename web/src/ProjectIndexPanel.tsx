// The project's code index at a glance: how much of its folder the index covers, as a bar and four
// numbers, and a button that brings it up to date. It stays quiet where the gateway has no such API.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { errorMessage } from "./errors";
import type { Api } from "./gitApi";
import {
  type IndexState,
  type ProjectIndexStatus,
  buildProjectIndex,
  getProjectIndex,
} from "./codeIndexApi";

const KB = 1024;

// stateLabel is what the badge says.
function stateLabel(state: IndexState): string {
  if (state === "ready") {
    return t("Up to date");
  }
  return choose(state === "stale", t("Out of date"), t("Not indexed"));
}

// Stat is one number under the bar.
function Stat({
  id,
  value,
  label,
}: {
  id: string;
  value: string;
  label: string;
}) {
  return (
    <div class="project-index-stat" data-testid={"project-index-" + id}>
      <span class="text-sm font-semibold text-[#e8e8ea]">{value}</span>
      <span class="text-xs text-[#7a7a8c]">{label}</span>
    </div>
  );
}

// IndexBody is the bar, the numbers and the notes for one status.
function IndexBody({ st }: { st: ProjectIndexStatus }) {
  const kb = Math.ceil(st.cache_bytes / KB);
  return (
    <>
      <div class="flex items-center gap-2">
        <progress
          class="settings-progress settings-progress-wide"
          data-state={st.state}
          max={100}
          value={st.percent}
          aria-label={t("Indexed share of the project")}
        />
        <span class="text-xs text-[#9a9aaa] tabular-nums">{st.percent}%</span>
      </div>
      <div class="project-index-stats">
        <Stat id="files" value={String(st.files)} label={t("files")} />
        <Stat id="chunks" value={String(st.chunks)} label={t("passages")} />
        <Stat id="terms" value={String(st.terms)} label={t("terms")} />
        <Stat id="size" value={kb + " KB"} label={t("on disk")} />
      </div>
      <p class="text-xs text-[#7a7a8c]" data-testid="project-index-note">
        {tf("{files} of {total} files covered.", {
          files: String(st.files),
          total: String(st.candidates),
        })}
        {st.capped &&
          " " +
            tf("The limit of {max} files is reached: raise it in Settings.", {
              max: String(st.max_files),
            })}
        {st.embeddings && " " + t("Ranked also by embeddings.")}
        {!st.embeddings && st.semantic && " " + t("Ranked also by meaning.")}
      </p>
    </>
  );
}

// ProjectIndexPanel shows the status of project `id` and lets the person update the index.
export function ProjectIndexPanel({ api, id }: { api: Api; id: string }) {
  const [st, setSt] = useState<ProjectIndexStatus | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    getProjectIndex(api, id)
      .then((v) => live && setSt(v))
      .catch((e) => live && setErr(errorMessage(e)));
    return () => {
      live = false;
    };
  }, [api, id]);

  const build = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      setSt(await buildProjectIndex(api, id));
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  }, [api, id]);

  return (
    <div data-testid="project-index" class="space-y-2">
      <div class="flex items-center gap-2">
        <span class="block text-sm text-[#9a9aaa]">{t("Code index")}</span>
        {st && (
          <span
            class="project-index-badge"
            data-state={st.state}
            data-testid="project-index-state"
          >
            {stateLabel(st.state)}
          </span>
        )}
        <button
          type="button"
          class="ml-auto px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 disabled:opacity-40"
          data-testid="project-index-build"
          disabled={busy || !st}
          onClick={build}
        >
          {choose(busy, t("Indexing…"), t("Index now"))}
        </button>
      </div>
      {st && <IndexBody st={st} />}
      {err && (
        <p
          class="text-xs text-danger"
          role="alert"
          data-testid="project-index-error"
        >
          {err}
        </p>
      )}
    </div>
  );
}
