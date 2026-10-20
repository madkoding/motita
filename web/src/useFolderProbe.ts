// useFolderProbe looks at the folder typed or picked for a new project: whether it exists, whether it
// sits among the system's own files. It asks the
// gateway's folder listing (see fsApi) after a short pause, so typing does not ask for every key.
import { useEffect, useState } from "preact/hooks";
import { listDirs, type DirListing } from "./fsApi";
import { choose } from "./choose";
import type { Api } from "./gitApi";

// FolderProbe is what is known of the path: "missing" is a folder that would be created.
export interface FolderProbe {
  // One of "idle", "exists", "sensitive", "missing" (it would be created) or "refused".
  state: string;
  // The reason the gateway gave when it refused to show the path at all.
  reason: string;
}

const IDLE: FolderProbe = { state: "idle", reason: "" };
export const PROBE_DELAY_MS = 350;

// parentOf is the path without its last name, keeping the separator of a root ("/" or "C:\").
export function parentOf(path: string): string {
  const cut = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
  const parent = path.slice(0, cut);
  return choose(
    parent === "" || parent.endsWith(":"),
    path.slice(0, cut + 1),
    parent,
  );
}

// probeOf reads the listing of path, or of its parent when path is not there yet.
async function probeOf(api: Api, path: string): Promise<FolderProbe> {
  let listing: DirListing;
  try {
    listing = await listDirs(api, path);
  } catch (e) {
    return missingOrRefused(api, path, (e as Error).message);
  }
  return {
    state: choose(listing.sensitive, "sensitive", "exists"),
    reason: "",
  };
}

// missingOrRefused tells a folder that does not exist yet, which would be created inside an
// existing parent, from a path the gateway refuses to show.
async function missingOrRefused(
  api: Api,
  path: string,
  reason: string,
): Promise<FolderProbe> {
  try {
    await listDirs(api, parentOf(path));
    return { state: "missing", reason };
  } catch {
    return { state: "refused", reason };
  }
}

export function useFolderProbe(api: Api | null, path: string): FolderProbe {
  const [probe, setProbe] = useState<FolderProbe>(IDLE);
  useEffect(() => {
    if (!api || !/^([/\\]|[A-Za-z]:[/\\])/.test(path)) {
      setProbe(IDLE);
      return undefined;
    }
    let live = true;
    const timer = setTimeout(() => {
      probeOf(api, path).then((p) => live && setProbe(p));
    }, PROBE_DELAY_MS);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [api, path]);
  return probe;
}
