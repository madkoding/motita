// useFolderPicker is the folder browser of the project dialogs: whether Settings → Security lets
// a project live outside the workspace (only then is the browser offered), and which dialog it was
// opened from, so the folder picked goes to that one.
import { useCallback, useEffect, useState } from "preact/hooks";
import { getSecurity } from "./securityApi";
import type { Api } from "./prApi";
import type { EditProjectDraft } from "./appTypes";

// FolderTarget is what the folder picked is for: the new project's folder, the edited project's, or
// a repository the project will use where it is.
export type FolderTarget = "new" | "edit" | "repo";

export function useFolderPicker(
  api: Api,
  open: boolean,
  setNewPath: (path: string) => void,
  patchEdit: (patch: Partial<EditProjectDraft>) => void,
  addRepoFolder: (path: string) => void,
) {
  const [fullDisk, setFullDisk] = useState(false);
  const [folderPicker, setFolderPicker] = useState<FolderTarget | null>(null);
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    let live = true;
    getSecurity(api)
      .then((v) => live && setFullDisk(v?.disk_access === "full"))
      .catch(() => live && setFullDisk(false));
    return () => {
      live = false;
    };
  }, [open]);
  const pickFolder = useCallback(
    (path: string) => {
      if (folderPicker === "edit") {
        patchEdit({ path });
      } else if (folderPicker === "repo") {
        addRepoFolder(path);
      } else {
        setNewPath(path);
      }
      setFolderPicker(null);
    },
    [folderPicker, patchEdit, setNewPath, addRepoFolder],
  );
  const closeFolderPicker = useCallback(() => setFolderPicker(null), []);
  return {
    fullDisk,
    folderPicker,
    openFolderPicker: setFolderPicker,
    pickFolder,
    closeFolderPicker,
  };
}
