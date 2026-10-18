// The slim, non-blocking notice shown while the page is open but the gateway stopped answering: the
// conversation stays readable, and the banner goes away by itself when the connection is back.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { IconAlert } from "./icons";

export function ConnectionBanner() {
  const { connection } = useApp().status;
  if (connection === "ok") {
    return null;
  }
  return (
    <div
      role="status"
      class="connection-banner fixed top-0 inset-x-0 z-90 flex items-center justify-center gap-2 px-3 py-1.5 text-xs text-white bg-danger/90"
    >
      <IconAlert size={14} />
      {choose(
        connection === "lost",
        t("No connection to the server"),
        t("Connection lost. Reconnecting…"),
      )}
    </div>
  );
}
