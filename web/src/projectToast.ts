// The toast shown once a project has been created.
import { tf } from "./i18n";
import { choose } from "./choose";
import type { Toast } from "./appTypes";

// createdToast is the toast shown once a project exists: a warning when some of its other
// repositories could not be added.
export function createdToast(
  title: string,
  failed: string[],
  cloneLog: unknown,
): Toast {
  if (failed.length > 0) {
    return {
      message: tf(
        'Project "{title}" created, but some repositories could not be added: {names}',
        { title, names: failed.join(", ") },
      ),
      type: "warning",
    };
  }
  return {
    message: tf(
      choose(
        cloneLog,
        'Project "{title}" created and repo cloned',
        'Project "{title}" created',
      ),
      { title },
    ),
    type: "success",
  };
}
