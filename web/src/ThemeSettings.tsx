import { useCallback, useState } from "preact/hooks";
import { t } from "./i18n";
import {
  loadTheme,
  saveTheme,
  MODES,
  SCHEMES,
  type Mode,
  type Scheme,
  type Theme,
} from "./theme";
import { Row, Segmented } from "./SettingsControls";

// The visible names. Written out so each one is a literal the translations can find.
function modeLabel(m: Mode): string {
  switch (m) {
    case "light":
      return t("Light");
    case "dark":
      return t("Dark");
    default:
      return t("Follow the system");
  }
}

function schemeLabel(s: Scheme): string {
  switch (s) {
    case "violet":
      return t("Violet");
    case "forest":
      return t("Forest");
    case "amber":
      return t("Amber");
    case "rose":
      return t("Rose");
    case "graphite":
      return t("Graphite");
    default:
      return "Motita";
  }
}

// ThemeSettings is the Themes tab: light, dark or the system's choice, and the colour scheme. Each
// scheme is a card that previews its own palette in the mode that is showing.
export function ThemeSettings() {
  const [theme, setTheme] = useState<Theme>(() => loadTheme());
  const update = (next: Theme) => {
    setTheme(next);
    saveTheme(next);
  };
  const setMode = useCallback(
    (mode: Mode) => update({ ...theme, mode }),
    [theme],
  );
  return (
    <section class="settings-group" aria-labelledby="settings-themes-h">
      <h3 id="settings-themes-h" class="settings-group-title">
        {t("Themes")}
      </h3>
      <div class="settings-card">
        <Row id="settings-mode" label={t("Mode")}>
          <Segmented<Mode>
            name="settings-mode"
            labelledBy="settings-mode"
            value={theme.mode}
            onChange={setMode}
            options={MODES.map((value) => ({
              value,
              label: modeLabel(value),
            }))}
          />
        </Row>
        <Row id="settings-scheme" label={t("Color scheme")} stack>
          <div
            role="radiogroup"
            aria-labelledby="settings-scheme"
            class="theme-grid"
          >
            {SCHEMES.map((scheme) => (
              <label key={scheme} class="theme-card" data-scheme={scheme}>
                <input
                  type="radio"
                  class="sr-only"
                  name="settings-scheme"
                  value={scheme}
                  checked={theme.scheme === scheme}
                  onChange={() => update({ ...theme, scheme })}
                />
                <span class="theme-preview" aria-hidden="true">
                  <span class="theme-preview-panel">
                    <span class="theme-preview-line" />
                    <span class="theme-preview-line short" />
                  </span>
                  <span class="theme-preview-accent" />
                </span>
                <span class="theme-name">{schemeLabel(scheme)}</span>
              </label>
            ))}
          </div>
        </Row>
      </div>
    </section>
  );
}
