// The artifacts modal: the files the agent saved in this session (or its project), and the one
// being looked at.
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { Markdown } from "./Markdown";
import { artifactShape, daysUntil } from "./appFormat";
import { IconClose } from "./icons";
import type { ArtifactItem, ArtifactScope, ArtifactView } from "./appTypes";
import { EXPIRY_WARN_DAYS, KB } from "./appConst";

const SCOPES: readonly ArtifactScope[] = ["session", "project"];

// ScopeTabs switch the list between the session's files and its project's.
function ScopeTabs() {
  const { artifactScope, pickScope } = useApp().artifacts;
  return (
    <div
      class="flex rounded-lg border border-white/10 overflow-hidden text-xs"
      role="tablist"
    >
      {SCOPES.map((sc) => (
        <button
          key={sc}
          role="tab"
          aria-selected={artifactScope === sc}
          class={`px-3 min-h-[30px] ${choose(artifactScope === sc, "bg-accent text-white", "text-[#c8c8d2] hover:bg-white/5")}`}
          onClick={() => pickScope(sc)}
        >
          {choose(sc === "session", t("This session"), t("Whole project"))}
        </button>
      ))}
    </div>
  );
}

// ListBar is the scope tabs, when the session has a project, and the upload.
function ListBar() {
  const { artifacts, selectedSession } = useApp();
  return (
    <div class="flex items-center gap-2 mb-3">
      {selectedSession?.project_id && <ScopeTabs />}
      <label class="ml-auto px-3 min-h-[30px] inline-flex items-center rounded-lg border border-white/10 text-xs text-[#c8c8d2] hover:bg-white/5 cursor-pointer">
        {t("Upload a file")}
        <input
          type="file"
          class="hidden"
          data-testid="artifact-upload"
          onChange={(e) => {
            const field = e.target as HTMLInputElement;
            const f = field.files?.[0];
            field.value = "";
            if (f) {
              void artifacts.uploadArtifact(f);
            }
          }}
        />
      </label>
    </div>
  );
}

// ArtifactBody is the file itself, drawn by its kind.
function ArtifactBody({ view }: { view: ArtifactView }) {
  const shape = artifactShape(view);
  if (shape === "pdf") {
    return (
      <iframe
        src={view.url}
        title={view.name}
        class="w-full flex-1 min-h-[60vh] rounded-lg bg-white"
      />
    );
  }
  if (shape === "image") {
    return (
      <img
        src={view.url}
        alt={view.name}
        class="max-w-full max-h-[60vh] object-contain rounded-lg"
      />
    );
  }
  if (shape === "markdown") {
    return (
      <div class="skill-body artifact-body overflow-auto flex-1 p-3 rounded-lg bg-black/30">
        <Markdown content={view.body || ""} />
      </div>
    );
  }
  if (shape === "html") {
    return (
      <iframe
        sandbox=""
        srcDoc={view.body}
        title={view.name}
        class="w-full flex-1 min-h-[50vh] rounded-lg bg-white"
      />
    );
  }
  return (
    <pre class="overflow-auto flex-1 text-xs font-mono p-3 rounded-lg bg-black/30 whitespace-pre-wrap">
      {view.body}
    </pre>
  );
}

// Viewer is one file, opened over the list.
function Viewer({ view }: { view: ArtifactView }) {
  const { setArtifactView } = useApp().artifacts;
  return (
    <div class="flex flex-col min-h-0 flex-1">
      <button
        class="self-start text-sm text-accent underline underline-offset-2 mb-2"
        onClick={() => setArtifactView(null)}
      >
        {t("Back to the list")}
      </button>
      <div class="text-sm font-mono text-[#c8c8d2] mb-2 truncate">
        {view.name}
      </div>
      <ArtifactBody view={view} />
    </div>
  );
}

// expiresSoon says the file will be deleted within the warning window, unless it is pinned.
function expiresSoon(a: ArtifactItem): boolean {
  return (
    !a.pinned && !!a.expires_at && daysUntil(a.expires_at) <= EXPIRY_WARN_DAYS
  );
}

// ArtifactRow is one file of the list: it opens, pins or deletes it.
function ArtifactRow({ a }: { a: ArtifactItem }) {
  const { openArtifact, pinArtifact, deleteArtifact } = useApp().artifacts;
  return (
    <li class="flex items-center gap-2 p-2.5 rounded-xl border border-white/10 bg-black/20">
      <button
        class="flex-1 min-w-0 text-left"
        onClick={() => void openArtifact(a)}
      >
        <div class="truncate text-sm text-[#e8e8ea] font-mono">{a.name}</div>
        <div class="text-xs text-[#7a7a8c]">
          {a.type} · {Math.max(1, Math.round(a.size / KB))} KB
        </div>
        {!!a.pinned && (
          <div class="text-xs text-accent">
            {t("Pinned: it will not be deleted")}
          </div>
        )}
        {expiresSoon(a) && (
          <div class="text-xs text-[#f0a040]" data-testid="artifact-expiry">
            {tf("Deleted in {n} days unless pinned", {
              // expiresSoon holds only for a file that has an expiry.
              n: String(Math.max(0, daysUntil(a.expires_at as string))),
            })}
          </div>
        )}
      </button>
      <button
        class="flex-none px-2 min-h-[32px] rounded-lg text-xs text-[#c8c8d2] hover:bg-white/5"
        aria-pressed={!!a.pinned}
        onClick={() => void pinArtifact(a.name, !a.pinned)}
      >
        {choose(a.pinned, t("Unpin"), t("Pin"))}
      </button>
      <button
        class="flex-none px-2 min-h-[32px] rounded-lg text-xs text-danger hover:bg-danger/10"
        onClick={() => void deleteArtifact(a.name)}
      >
        {t("Delete")}
      </button>
    </li>
  );
}

// ArtifactList is the files of the scope, or what to do when there are none.
function ArtifactList() {
  const { artifacts } = useApp().artifacts;
  if (artifacts.length === 0) {
    return (
      <p class="text-sm text-[#9a9aaa]">
        {t(
          "Nothing saved in this session yet. Ask for a report, a page or a diagram and it will show up here.",
        )}
      </p>
    );
  }
  return (
    <ul class="overflow-auto space-y-1.5">
      {artifacts.map((a) => (
        <ArtifactRow key={a.name} a={a} />
      ))}
    </ul>
  );
}

export function ArtifactsModal() {
  const { artifactView, closeArtifacts } = useApp().artifacts;
  return (
    <ModalBackdrop onClose={closeArtifacts}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-2xl p-5 shadow-2xl max-h-[85vh] flex flex-col">
        <div class="flex items-center gap-2 mb-4">
          <h2 class="text-base font-semibold">{t("Artifacts")}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeArtifacts}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        {!artifactView && <ListBar />}
        {!!artifactView && <Viewer view={artifactView} />}
        {!artifactView && <ArtifactList />}
      </div>
    </ModalBackdrop>
  );
}
