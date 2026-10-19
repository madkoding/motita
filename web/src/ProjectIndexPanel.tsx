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
  sizeLabel,
} from "./codeIndexApi";

// While the gateway builds the index in the background, the panel asks again this often.
const POLL_MS = 3000;

// stateLabel is what the badge says.
function stateLabel(state: IndexState, indexing: boolean): string {
  if (indexing) {
    return t("Indexing…");
  }
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

// coverage says how many files the index holds. A ready index holds every text file, so the rest
// are binaries it skipped; an old or missing one is measured against what the folder offers.
function coverage(st: ProjectIndexStatus): string {
  if (st.state === "ready") {
    return tf(
      "{files} files indexed; {skipped} binary or unreadable skipped.",
      {
        files: String(st.files),
        skipped: String(Math.max(0, st.candidates - st.files)),
      },
    );
  }
  return tf("{files} of {total} files covered.", {
    files: String(st.files),
    total: String(st.candidates),
  });
}

// IndexBody is the bar, the numbers and the notes for one status.
function IndexBody({ st }: { st: ProjectIndexStatus }) {
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
        <Stat
          id="size"
          value={sizeLabel(st.cache_bytes)}
          label={t("on disk")}
        />
      </div>
      <p class="text-xs text-[#7a7a8c]" data-testid="project-index-note">
        {coverage(st)}
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
  const [done, setDone] = useState("");

  useEffect(() => {
    let live = true;
    getProjectIndex(api, id)
      .then((v) => live && setSt(v))
      .catch((e) => live && setErr(errorMessage(e)));
    return () => {
      live = false;
    };
  }, [api, id]);

  useEffect(() => {
    if (!st?.indexing) {
      return undefined;
    }
    const timer = setTimeout(() => {
      getProjectIndex(api, id)
        .then(setSt)
        .catch(() => undefined);
    }, POLL_MS);
    return () => clearTimeout(timer);
  }, [api, id, st]);

  const build = useCallback(async () => {
    setBusy(true);
    setErr("");
    setDone("");
    const wasReady = st?.state === "ready";
    try {
      const next = await buildProjectIndex(api, id);
      setSt(next);
      setDone(
        choose(
          wasReady,
          t("Already up to date."),
          tf("Index updated: {files} files, {chunks} passages.", {
            files: String(next.files),
            chunks: String(next.chunks),
          }),
        ),
      );
    } catch (e) {
      setErr(errorMessage(e));
    }
    setBusy(false);
  }, [api, id, st]);

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
            {stateLabel(st.state, st.indexing)}
          </span>
        )}
        <button
          type="button"
          class="ml-auto px-3 min-h-[36px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 disabled:opacity-40"
          data-testid="project-index-build"
          disabled={busy || !st || st.indexing}
          onClick={build}
        >
          {choose(busy, t("Indexing…"), t("Index now"))}
        </button>
      </div>
      {st && <IndexBody st={st} />}
      {busy && (
        <output
          class="block text-xs text-[#9a9aaa]"
          data-testid="project-index-working"
        >
          <progress
            class="settings-progress settings-progress-wide mb-1"
            aria-label={t("Indexing…")}
          />
          {t("Reading the project's files…")}
        </output>
      )}
      {done && !busy && !err && (
        <output
          class="block text-xs text-accent"
          data-testid="project-index-done"
        >
          {done}
        </output>
      )}
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
