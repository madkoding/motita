import { IconAlert } from "./icons";
import { t } from "./i18n";

// InsecureWarning is the amber pill a setting shows when the page was opened without a secure
// connection, so the browser hides the feature. It says that in plain words; the technical reason
// (https or localhost) and the way out stay folded under "Why?" for whoever wants them.
export function InsecureWarning({
  text,
  guide,
  why,
}: {
  text: string;
  guide: string;
  // why replaces the folded explanation, for a setting the browser does not block but that is
  // still exposed on an insecure page.
  why?: string;
}) {
  return (
    <div class="settings-warn" role="note" data-testid="insecure-warning">
      <div class="settings-warn-title">
        <IconAlert size={14} class="settings-warn-icon" />
        {t("Connection not secure")}
      </div>
      <p class="settings-warn-text">{text}</p>
      <details class="settings-warn-more">
        <summary>{t("Why?")}</summary>
        <p>
          {why ||
            t(
              "Browsers only allow it on a secure address: one that starts with https://, or localhost on this same computer. Open Motita through https to use it.",
            )}{" "}
          <a href={guide} target="_blank" rel="noopener noreferrer">
            {t("How to set it up")}
          </a>
        </p>
      </details>
    </div>
  );
}
