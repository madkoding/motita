// Artifacts: the files the agent saved for the person in the selected session (or its project),
// and the one being looked at.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { choose } from "./choose";
import type { Api } from "./prApi";
import type {
  ArtifactItem,
  ArtifactScope,
  ArtifactView,
  SetStatus,
} from "./appTypes";
import {
  DOWNLOAD_URL_TTL_MS,
  HTTP_NO_CONTENT,
  JSON_HEADERS,
  SESSIONS_PATH,
} from "./appConst";

// jsonOrEmpty reads a response's JSON body, or an empty object when the request failed.
async function jsonOrEmpty(res: Response) {
  if (!res.ok) {
    return {};
  }
  return res.json();
}

const scopeQuery = (scope: ArtifactScope) =>
  choose(scope === "project", "?scope=project", "");

// refused says whether a change to an artifact was refused: 204 is a success with no body.
function refused(res: Response): boolean {
  return !res.ok && res.status !== HTTP_NO_CONTENT;
}

// download saves a file the page cannot show.
function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), DOWNLOAD_URL_TTL_MS);
}

export function useArtifacts(api: Api, sessionId: string, setState: SetStatus) {
  const [showArtifacts, setShowArtifacts] = useState(false);
  const [artifactScope, setArtifactScope] = useState<ArtifactScope>("session");
  const [artifacts, setArtifacts] = useState<ArtifactItem[]>([]);
  const [artifactView, setArtifactView] = useState<ArtifactView | null>(null);
  // sidRef is the session on screen now: an answer that arrives after the person moved to
  // another session belongs to the old one and is dropped instead of shown over the new one.
  const sidRef = useRef(sessionId);
  sidRef.current = sessionId;
  const moved = (sid: string) => sidRef.current !== sid;

  const artifactBase = useCallback(
    () => SESSIONS_PATH + encodeURIComponent(sessionId) + "/artifacts",
    [sessionId],
  );

  const loadArtifacts = useCallback(
    async (scope: ArtifactScope = artifactScope) => {
      if (!sessionId) {
        setArtifacts([]);
        return;
      }
      const sid = sessionId;
      let list: ArtifactItem[];
      try {
        const res = await api(artifactBase() + scopeQuery(scope));
        const d = await jsonOrEmpty(res);
        list = choose(Array.isArray(d.artifacts), d.artifacts, []);
      } catch {
        list = [];
      }
      if (!moved(sid)) {
        setArtifacts(list);
      }
    },
    [sessionId, artifactScope, artifactBase],
  );

  // openArtifact fetches one file with the token (a plain link would not carry it) and shows it
  // by kind: pages in a sandboxed frame, Markdown rendered, images and PDFs as themselves, text as
  // text; anything else is saved.
  const openArtifact = useCallback(
    async (
      a: { name: string; type: string },
      scope: ArtifactScope = artifactScope,
    ) => {
      const sid = sessionId;
      // show puts the file on screen, unless the session changed while it was fetched.
      const show = (view: () => ArtifactView) => {
        if (!moved(sid)) {
          setArtifactView(view());
        }
      };
      try {
        const res = await api(
          artifactBase() + "/" + encodeURIComponent(a.name) + scopeQuery(scope),
        );
        if (!res.ok) {
          setState("could not open the artifact", true);
          return;
        }
        if (
          a.type.startsWith("image/") ||
          a.type.startsWith("application/pdf")
        ) {
          const blob = await res.blob();
          show(() => ({
            name: a.name,
            type: a.type,
            url: URL.createObjectURL(blob),
          }));
        } else if (
          a.type.startsWith("text/") ||
          a.type === "application/json"
        ) {
          const body = await res.text();
          show(() => ({ name: a.name, type: a.type, body }));
        } else {
          download(await res.blob(), a.name);
        }
      } catch {
        setState("could not open the artifact", true);
      }
    },
    [sessionId, artifactScope, artifactBase],
  );

  // A new session starts with nothing of the old one on screen: its list and the file being
  // looked at go, and an open list is loaded again for the session now shown.
  useEffect(() => {
    setArtifacts([]);
    setArtifactView(null);
    if (showArtifacts) {
      void loadArtifacts();
    }
  }, [sessionId]);

  const deleteArtifact = useCallback(
    async (name: string) => {
      try {
        const res = await api(
          artifactBase() +
            "/" +
            encodeURIComponent(name) +
            scopeQuery(artifactScope),
          { method: "DELETE" },
        );
        if (refused(res)) {
          setState("could not delete the artifact", true);
          return;
        }
        setArtifactView((v) => choose(v?.name === name, null, v));
        await loadArtifacts();
      } catch {
        setState("could not delete the artifact", true);
      }
    },
    [artifactScope, artifactBase, loadArtifacts],
  );

  // pinArtifact keeps a file out of the retention, or lets it expire again.
  const pinArtifact = useCallback(
    async (name: string, pinned: boolean) => {
      try {
        const res = await api(
          artifactBase() +
            "/" +
            encodeURIComponent(name) +
            "/pin" +
            scopeQuery(artifactScope),
          {
            method: "POST",
            headers: JSON_HEADERS,
            body: JSON.stringify({ pinned }),
          },
        );
        if (refused(res)) {
          setState("could not pin the artifact", true);
          return;
        }
        await loadArtifacts();
      } catch {
        setState("could not pin the artifact", true);
      }
    },
    [artifactScope, artifactBase, loadArtifacts],
  );

  // uploadArtifact saves a file the person picks into this session, as the raw body.
  const uploadArtifact = useCallback(
    async (file: File) => {
      try {
        const res = await api(
          artifactBase() + "/" + encodeURIComponent(file.name),
          { method: "PUT", body: file },
        );
        if (!res.ok) {
          const err = await res.json().catch(() => ({}));
          setState(err.error || "could not save the file", true);
          return;
        }
        setArtifactScope("session");
        await loadArtifacts("session");
      } catch {
        setState("could not save the file", true);
      }
    },
    [artifactBase, loadArtifacts],
  );

  // openArtifacts shows the session's files, from the list.
  const openArtifacts = () => {
    setArtifactView(null);
    setArtifactScope("session");
    setShowArtifacts(true);
    void loadArtifacts("session");
  };

  // showRunArtifact opens one file a run saved, over the session's list.
  const showRunArtifact = (a: { name: string; type: string }) => {
    setArtifactScope("session");
    setArtifactView(null);
    setShowArtifacts(true);
    void loadArtifacts("session");
    void openArtifact(a, "session");
  };

  // pickScope switches the list between the session's files and the project's.
  const pickScope = (scope: ArtifactScope) => {
    setArtifactScope(scope);
    void loadArtifacts(scope);
  };

  const closeArtifacts = useCallback(() => setShowArtifacts(false), []);

  return {
    showArtifacts,
    closeArtifacts,
    artifactScope,
    artifacts,
    artifactView,
    setArtifactView,
    artifactBase,
    openArtifact,
    deleteArtifact,
    pinArtifact,
    uploadArtifact,
    openArtifacts,
    showRunArtifact,
    pickScope,
  };
}

export type Artifacts = ReturnType<typeof useArtifacts>;
