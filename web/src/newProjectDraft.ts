// What the New project dialog holds, read as the project it would make: the title (the name typed,
// or the folder's, or the repository's), the folder in the workspace, the repository to clone and
// whether that is enough to create it. The dialog and the request it sends read it the same way.
import { choose } from "./choose";
import { folderNameOf, repoNameOfUrl, slugOf } from "./appParse";

export interface NewProjectFields {
  newProjectTitle: string;
  newProjectDir: string;
  newProjectPath: string;
  newProjectSource: "folder" | "clone";
  newProjectGitUrl: string;
}

export function newProjectDraft(f: NewProjectFields) {
  const gitUrl = choose(
    f.newProjectSource === "clone",
    f.newProjectGitUrl.trim(),
    "",
  );
  const title =
    f.newProjectTitle.trim() ||
    folderNameOf(f.newProjectPath) ||
    repoNameOfUrl(gitUrl);
  const dir = f.newProjectDir.trim() || slugOf(title);
  const placed = !!(dir || f.newProjectPath);
  const cloned = f.newProjectSource !== "clone" || !!gitUrl;
  return { title, dir, gitUrl, ready: !!title && placed && cloned };
}
