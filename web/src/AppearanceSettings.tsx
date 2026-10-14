import { useCallback, useState } from "preact/hooks";
import { t, type LangSetting } from "./i18n";
import {
  initSettings,
  saveSettings,
  type UISettings,
  type FontSize,
  type Density,
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
      </div>
    </section>
  );
}
