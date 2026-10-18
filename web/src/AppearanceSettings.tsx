import { useCallback, useState } from "preact/hooks";
import { t, type LangSetting } from "./i18n";
import {
  initSettings,
  saveSettings,
  type UISettings,
  type FontSize,
  type Density,
  loadNextHint,
  saveNextHint,
  loadUsageStyle,
  saveUsageStyle,
  type UsageStyle,
} from "./settings";
import { Row, Segmented } from "./SettingsControls";

export interface AppearanceSettingsProps {
  langSetting: LangSetting;
  langAvailable: boolean;
  onLang: (s: LangSetting) => void;
}

// AppearanceSettings is how the page looks: language, font size and density.
export function AppearanceSettings({
  langSetting,
  langAvailable,
  onLang,
}: AppearanceSettingsProps) {
  const [s, setS] = useState<UISettings>(() => initSettings());
  const [hint, setHint] = useState(() => loadNextHint());
  const [usageStyle, setUsageStyle] = useState(() => loadUsageStyle());
  const changeUsageStyle = useCallback((v: UsageStyle) => {
    setUsageStyle(v);
    saveUsageStyle(v);
  }, []);
  const update = (next: UISettings) => {
    setS(next);
    saveSettings(next);
  };
  const setFontSize = useCallback(
    (fontSize: FontSize) => update({ ...s, fontSize }),
    [s],
  );
  const setDensity = useCallback(
    (density: Density) => update({ ...s, density }),
    [s],
  );
  return (
    <section class="settings-group" aria-labelledby="settings-look-h">
      <h3 id="settings-look-h" class="settings-group-title">
        {t("Appearance")}
      </h3>
      <div class="settings-card">
        {langAvailable && (
          <Row id="settings-lang" label={t("Language")}>
            <Segmented<LangSetting>
              name="settings-lang"
              labelledBy="settings-lang"
              value={langSetting}
              onChange={onLang}
              options={[
                { value: "auto", label: t("Automatic") },
                { value: "en", label: "English" },
                { value: "es", label: "Español" },
              ]}
            />
          </Row>
        )}
        <Row id="settings-font" label={t("Font size")}>
          <Segmented<FontSize>
            name="settings-font"
            labelledBy="settings-font"
            value={s.fontSize}
            onChange={setFontSize}
            options={[
              { value: "small", label: t("Small") },
              { value: "normal", label: t("Normal") },
              { value: "large", label: t("Large") },
            ]}
          />
        </Row>
        <Row id="settings-density" label={t("Density")}>
          <Segmented<Density>
            name="settings-density"
            labelledBy="settings-density"
            value={s.density}
            onChange={setDensity}
            options={[
              { value: "comfortable", label: t("Comfortable") },
              { value: "compact", label: t("Compact") },
            ]}
          />
        </Row>
        <Row id="settings-usage-style" label={t("Provider usage")}>
          <Segmented<UsageStyle>
            name="settings-usage-style"
            labelledBy="settings-usage-style"
            value={usageStyle}
            onChange={changeUsageStyle}
            options={[
              { value: "bars", label: t("Bars") },
              { value: "gauge", label: t("Gauge") },
            ]}
          />
        </Row>
        <Row
          id="settings-next-hint"
          label={t("Suggest the next request")}
          hint={t("After a turn, the empty field hints at what to ask next.")}
        >
          <label class="settings-switch-hit">
            <input
              type="checkbox"
              role="switch"
              class="settings-switch"
              aria-labelledby="settings-next-hint"
              checked={hint}
              onChange={(e) => {
                const v = (e.target as HTMLInputElement).checked;
                setHint(v);
                saveNextHint(v);
              }}
            />
          </label>
        </Row>
      </div>
    </section>
  );
}
