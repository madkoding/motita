// The toast shown once a project has been created.
import { tf } from "./i18n";
import type { Toast } from "./appTypes";

// createdToast is the toast shown once a project exists.
export function createdToast(title: string): Toast {
  return {
    message: tf('Project "{title}" created', { title }),
    type: "success",
  };
}
