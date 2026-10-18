// The read-aloud section of the settings: when answers are read, by which voice, how fast and how
// much.
import { useCallback, useEffect, useState } from "preact/hooks";
import { lang, t } from "./i18n";
import type { Lang } from "./i18n";
import { choose } from "./choose";
import type { Api } from "./gitApi";
import { LocalVoices } from "./LocalVoiceSettings";
import {
  LENGTHS,
  MEDIUM_CHARS,
  PITCH_RANGE,
  VOLUME_RANGE,
  VARIATION_RANGE,
  RATES,
  SHORT_CHARS,
  TUNE_RANGE,
  type SpeakMode,
  type SpeakStyle,
  type SpeechEngine,
  type SpeechPrefs,
  type SubtitleSize,
  type VoiceLike,
  formatPronunciations,
  parsePronunciations,
  voicesFor,
} from "./speech";
import {
  Row,
  Segmented,
  inputCls,
  selectCls,
  smallBtn,
} from "./SettingsControls";

export interface SpeechSettingsProps {
  supported: boolean;
  prefs: SpeechPrefs;
  setPrefs: (p: SpeechPrefs) => void;
  voices: VoiceLike[];
  test: () => void;
  // device says this device has voices of its own; gatewayVoices are the gateway's (voice.tts),
  // empty when it has none.
  device?: boolean;
  gatewayVoices?: string[];
  // allVoices are the device's voices in every language, for the voice of each language;
  // canListen says speech input works here, which conversation mode needs.
  allVoices?: VoiceLike[];
  canListen?: boolean;
  // local says the gateway offers local voices this browser can run; api reaches them.
  local?: boolean;
  api?: Api;
}

// SLIDER_STEP is how far one step of a voice slider moves.
const SLIDER_STEP = 0.1;
// VOLUME_STEP is one step of the volume slider: 5 %.
const VOLUME_STEP = 0.05;

// VoiceSlider is one attribute of the voice on a slider, with its value beside it and a way
// back to the voice's own (1).
function VoiceSlider({
  id,
  label,
  hint,
  range,
  value,
  disabled,
  format = rateLabel,
  resetTo = 1,
  step = SLIDER_STEP,
  onChange,
}: {
  id: string;
  label: string;
  hint?: string;
  range: [number, number];
  value: number;
  disabled?: boolean;
  format?: (v: number) => string;
  resetTo?: number;
  step?: number;
  onChange: (v: number) => void;
}) {
  return (
    <Row id={id} label={label} hint={hint} stack>
      <div class="flex items-center gap-3 w-full">
        <input
          type="range"
          class="flex-1 accent-[var(--color-accent,#4cc2ff)]"
          aria-labelledby={id}
          data-testid={id}
          min={range[0]}
          max={range[1]}
          step={step}
          value={value}
          disabled={disabled}
          onInput={(e) =>
            onChange(Number((e.target as HTMLInputElement).value))
          }
        />
        <span class="text-xs text-[#a0a0b0] w-12 text-right tabular-nums">
          {format(value)}
        </span>
        <button
          type="button"
          class={smallBtn}
          disabled={disabled || value === resetTo}
          data-testid={id + "-reset"}
          onClick={() => onChange(resetTo)}
        >
          {t("Reset")}
        </button>
      </div>
    </Row>
  );
}

// VoiceTuning are the sliders of the voice that speaks: expressiveness and rhythm for a downloaded
// Piper voice, the pitch for the device's voices. The gateway's voice has none.
function VoiceTuning({
  localChosen,
  gateway,
  off,
  prefs,
  set,
}: {
  localChosen: boolean;
  gateway: boolean;
  off: boolean;
  prefs: SpeechPrefs;
  set: (patch: Partial<SpeechPrefs>) => void;
}) {
  // Handed to VoiceSlider, so they keep their identity between renders.
  const setExpressiveness = useCallback(
    (expressiveness: number) => set({ expressiveness }),
    [set],
  );
  const setVariation = useCallback(
    (variation: number) => set({ variation }),
    [set],
  );
  const setPitch = useCallback((pitch: number) => set({ pitch }), [set]);
  if (localChosen) {
    return (
      <>
        <VoiceSlider
          id="settings-speech-expressiveness"
          label={t("Voice texture")}
          hint={t(
            "How smooth the voice sounds: lower is cleaner and steadier, higher is rougher.",
          )}
          range={TUNE_RANGE}
          value={prefs.expressiveness}
          onChange={setExpressiveness}
        />
        <VoiceSlider
          id="settings-speech-variation"
          label={t("Rhythm variation")}
          hint={t(
            "How much the length of each sound varies: lower is more even, higher more natural.",
          )}
          range={VARIATION_RANGE}
          value={prefs.variation}
          onChange={setVariation}
        />
      </>
    );
  }
  if (gateway) {
    return null;
  }
  return (
    <VoiceSlider
      id="settings-speech-pitch"
      label={t("Pitch")}
      range={PITCH_RANGE}
      value={prefs.pitch}
      disabled={off}
      onChange={setPitch}
    />
  );
}

// volumeLabel writes a volume as a percentage: 80 %.
function volumeLabel(v: number): string {
  return Math.round(v * 100).toLocaleString(lang()) + " %";
}

// rateLabel writes a speed as people say it: 1×, 1.25×.
function rateLabel(r: number): string {
  return r.toLocaleString(lang()) + "×";
}

// lengthLabel names the longest answer read whole.
function lengthLabel(n: number): string {
  if (n === 0) {
    return t("Whole");
  }
  if (n <= SHORT_CHARS) {
    return t("Short");
  }
  if (n <= MEDIUM_CHARS) {
    return t("Medium");
  }
  return t("Long");
}

// engineOptions are the voices that can read aloud here, in the order the switch shows them.
function engineOptions(
  device: boolean,
  hasGateway: boolean,
  hasLocal: boolean,
): EngineOption[] {
  // Each engine is named by where its voice comes from, and said what that means for the text.
  const engines: EngineOption[] = [];
  if (device) {
    engines.push({
      value: "device",
      label: t("System voices"),
      about: t(
        "The voices this device or browser already has. Nothing leaves the browser.",
      ),
    });
  }
  if (hasGateway) {
    engines.push({
      value: "gateway",
      label: t("Voice service"),
      about: t(
        "The speech service set up on the gateway: the same voice on every device. The text of each answer read goes to that service.",
      ),
    });
  }
  if (hasLocal) {
    engines.push({
      value: "local",
      label: t("Downloaded voice"),
      about: t(
        "A natural voice the gateway downloads once; it then runs inside this browser, and nothing leaves the device.",
      ),
    });
  }
  return engines;
}

interface EngineOption {
  value: SpeechEngine;
  label: string;
  about: string;
}

// EngineAbout says what each engine means for the text, the one that speaks highlighted.
function EngineAbout({
  engines,
  shown,
}: {
  engines: EngineOption[];
  shown: SpeechEngine;
}) {
  return (
    <ul
      class="flex flex-col gap-0.5"
      data-testid="settings-speech-engine-about"
    >
      {engines.map((e) => (
        <li
          key={e.value}
          class={choose(e.value === shown, "text-[#c8c8d0]", "")}
        >
          <span class="font-medium">{e.label}:</span> {e.about}
        </li>
      ))}
    </ul>
  );
}

// whoSpeaks says which voice reads. A local voice speaks when it is chosen; the gateway when it is
// chosen, or when the device has no voices of its own.
function whoSpeaks(
  engine: SpeechEngine,
  device: boolean,
  hasGateway: boolean,
  hasLocal: boolean,
): { localChosen: boolean; gateway: boolean } {
  const localChosen = hasLocal && engine === "local";
  const gateway =
    !localChosen && hasGateway && (engine === "gateway" || !device);
  return { localChosen, gateway };
}

// engineShownOf is the voice that speaks.
function engineShownOf(localChosen: boolean, gateway: boolean): SpeechEngine {
  if (localChosen) {
    return "local";
  }
  if (gateway) {
    return "gateway";
  }
  return "device";
}

// SpeechSettings is reading answers aloud: when, who speaks (the device, or the gateway's voice
// when it has one), which voice, how fast and how much. With the device's voices it needs neither
// the microphone nor the gateway, so it works on any address.
export function SpeechSettings({
  supported,
  prefs,
  setPrefs,
  voices,
  test,
  device = true,
  gatewayVoices = [],
  allVoices = [],
  canListen = false,
  local = false,
  api,
}: SpeechSettingsProps) {
  const set = useCallback(
    (patch: Partial<SpeechPrefs>) => setPrefs({ ...prefs, ...patch }),
    [prefs, setPrefs],
  );
  // Handed to Segmented, so they keep their identity between renders.
  const setMode = useCallback((mode: SpeakMode) => set({ mode }), [set]);
  const setStyle = useCallback((style: SpeakStyle) => set({ style }), [set]);
  const setEngine = useCallback(
    (engine: SpeechEngine) => set({ engine }),
    [set],
  );
  const hasGateway = gatewayVoices.length > 0;
  const hasLocal = local && !!api;
  const { localChosen, gateway } = whoSpeaks(
    prefs.engine,
    device,
    hasGateway,
    hasLocal,
  );
  const engines = engineOptions(device, hasGateway, hasLocal);
  const engineShown = engineShownOf(localChosen, gateway);
  const setVolume = useCallback((v: number) => set({ volume: v }), [set]);
  const setRate = useCallback((r: string) => set({ rate: Number(r) }), [set]);
  const setLength = useCallback(
    (n: string) => set({ maxChars: Number(n) }),
    [set],
  );
  const setConversation = useCallback(
    (conversation: boolean) => set({ conversation }),
    [set],
  );
  const setNotices = useCallback((notices: boolean) => set({ notices }), [set]);
  const setSubtitles = useCallback(
    (subtitles: boolean) => set({ subtitles }),
    [set],
  );
  const setSubtitleSize = useCallback(
    (subtitleSize: SubtitleSize) => set({ subtitleSize }),
    [set],
  );
  const off = !supported;
  return (
    <section class="settings-group" aria-labelledby="settings-speech-h">
      <h3 id="settings-speech-h" class="settings-group-title">
        {t("Read aloud")}
      </h3>
      <div class="settings-card" data-testid="speech-setting">
        <Row
          id="settings-speak"
          label={t("Read answers aloud")}
          stack
          hint={choose(
            off,
            t("This browser cannot read aloud."),
            t("Alt+L or 🔊 on an answer reads it or stops."),
          )}
        >
          <Segmented<SpeakMode>
            name="settings-speak"
            labelledBy="settings-speak"
            value={prefs.mode}
            disabled={off}
            onChange={setMode}
            options={[
              { value: "off", label: t("Off") },
              { value: "voice", label: t("When I spoke") },
              { value: "always", label: t("Always") },
            ]}
          />
        </Row>
        <Row
          id="settings-speak-style"
          label={t("What it says")}
          stack
          hint={answersHint(prefs.mode, styleHint(prefs.style))}
        >
          <Segmented<SpeakStyle>
            name="settings-speak-style"
            labelledBy="settings-speak-style"
            value={prefs.style}
            disabled={off || prefs.mode === "off"}
            onChange={setStyle}
            options={[
              { value: "literal", label: t("Word for word") },
              { value: "natural", label: t("Natural") },
            ]}
          />
        </Row>
        {engines.length > 1 && (
          <Row
            id="settings-speech-engine"
            label={t("Who speaks")}
            stack
            hint={<EngineAbout engines={engines} shown={engineShown} />}
          >
            <Segmented<SpeechEngine>
              name="settings-speech-engine"
              labelledBy="settings-speech-engine"
              value={engineShown}
              onChange={setEngine}
              options={engines}
            />
          </Row>
        )}
        <SpeechVoice
          engine={engineShown}
          api={api}
          off={off}
          prefs={prefs}
          set={set}
          voices={voices}
          gatewayVoices={gatewayVoices}
        />
        <Row
          id="settings-speech-rate"
          label={t("Speed")}
          stack
          hint={rateHint(prefs)}
        >
          <Segmented<string>
            name="settings-speech-rate"
            labelledBy="settings-speech-rate"
            value={String(prefs.rate)}
            disabled={off || speaksNothing(prefs)}
            onChange={setRate}
            options={RATES.map((r) => ({
              value: String(r),
              label: rateLabel(r),
            }))}
          />
        </Row>
        <VoiceSlider
          id="settings-speech-volume"
          label={t("Volume")}
          range={VOLUME_RANGE}
          value={prefs.volume}
          step={VOLUME_STEP}
          format={volumeLabel}
          disabled={off || speaksNothing(prefs)}
          onChange={setVolume}
        />
        <VoiceTuning
          localChosen={localChosen}
          gateway={gateway}
          off={off}
          prefs={prefs}
          set={set}
        />
        <Row
          id="settings-speech-length"
          label={t("Length")}
          stack
          hint={answersHint(
            prefs.mode,
            t("A longer answer is read up to its first paragraph."),
          )}
        >
          <Segmented<string>
            name="settings-speech-length"
            labelledBy="settings-speech-length"
            value={String(prefs.maxChars)}
            disabled={off || prefs.mode === "off"}
            onChange={setLength}
            options={LENGTHS.map((n) => ({
              value: String(n),
              label: lengthLabel(n),
            }))}
          />
        </Row>
        {!gateway && (
          <LanguageVoice
            off={off}
            prefs={prefs}
            set={set}
            allVoices={allVoices}
          />
        )}
        <Row
          id="settings-speech-conversation"
          label={t("Conversation")}
          hint={conversationHint(canListen, prefs.mode)}
        >
          <SpeechSwitch
            id="settings-speech-conversation"
            usable={!off && canListen && prefs.mode !== "off"}
            checked={prefs.conversation}
            onChange={setConversation}
          />
        </Row>
        <Row
          id="settings-speech-notices"
          label={t("Voice notices")}
          hint={t(
            "Says the CI of a pull request and the questions the agent waits on, while this tab is visible.",
          )}
        >
          <SpeechSwitch
            id="settings-speech-notices"
            usable={!off}
            checked={prefs.notices}
            onChange={setNotices}
          />
        </Row>
        <Row
          id="settings-speech-subtitles"
          label={t("Subtitles")}
          hint={t(
            "Shows on screen what Motita says aloud, sentence by sentence, like a film's subtitles.",
          )}
        >
          <SpeechSwitch
            id="settings-speech-subtitles"
            usable={!off}
            checked={prefs.subtitles}
            onChange={setSubtitles}
          />
        </Row>
        <Row
          id="settings-speech-subtitle-size"
          label={t("Subtitle size")}
          stack
          hint={choose(prefs.subtitles, undefined, t("Needs subtitles on."))}
        >
          <Segmented<SubtitleSize>
            name="settings-speech-subtitle-size"
            labelledBy="settings-speech-subtitle-size"
            value={prefs.subtitleSize}
            disabled={off || !prefs.subtitles}
            onChange={setSubtitleSize}
            options={[
              { value: "small", label: t("Small") },
              { value: "medium", label: t("Normal") },
              { value: "large", label: t("Large") },
            ]}
          />
        </Row>
        <Pronunciations off={off} prefs={prefs} set={set} />
        <Row id="settings-speech-test" label={t("Try the voice")}>
          <button
            type="button"
            class={smallBtn}
            disabled={off}
            onClick={test}
            data-testid="speech-test"
          >
            {t("Try")}
          </button>
        </Row>
      </div>
    </section>
  );
}

// SpeechVoice picks the voice of the one that speaks: a local voice, one of the gateway's, or
// one of the device's.
function SpeechVoice({
  engine,
  api,
  off,
  prefs,
  set,
  voices,
  gatewayVoices,
}: {
  engine: SpeechEngine;
  api: Api | undefined;
  off: boolean;
  prefs: SpeechPrefs;
  set: (patch: Partial<SpeechPrefs>) => void;
  voices: VoiceLike[];
  gatewayVoices: string[];
}) {
  const setLocalVoice = useCallback(
    (localVoice: string) => set({ localVoice }),
    [set],
  );
  if (engine === "local") {
    if (!api) {
      return null;
    }
    return (
      <Row id="settings-speech-local" label={t("Voices to download")} stack>
        <LocalVoices
          api={api}
          chosen={prefs.localVoice}
          choose={setLocalVoice}
        />
      </Row>
    );
  }
  if (engine === "gateway") {
    return (
      <Row id="settings-speech-voice" label={t("Voice")}>
        <select
          class={selectCls}
          aria-labelledby="settings-speech-voice"
          value={prefs.gatewayVoice}
          onChange={(e) =>
            set({ gatewayVoice: (e.target as HTMLSelectElement).value })
          }
        >
          <option value="">{t("Gateway default")}</option>
          {gatewayVoices.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
      </Row>
    );
  }
  return (
    <Row
      id="settings-speech-voice"
      label={t("Voice")}
      hint={choose(
        !off && voices.length === 0,
        t("This device has no voice for the interface language."),
        undefined,
      )}
    >
      <select
        class={selectCls}
        aria-labelledby="settings-speech-voice"
        disabled={off}
        value={prefs.voiceURI}
        onChange={(e) =>
          set({ voiceURI: (e.target as HTMLSelectElement).value })
        }
      >
        <option value="">{t("Device default")}</option>
        {voices.map((v) => (
          <option key={v.voiceURI} value={v.voiceURI}>
            {v.name}
          </option>
        ))}
      </select>
    </Row>
  );
}

// SpeechSwitch is one switch of the read-aloud section.
function SpeechSwitch({
  id,
  usable,
  checked,
  onChange,
}: {
  id: string;
  usable: boolean;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label class="settings-switch-hit">
      <input
        type="checkbox"
        role="switch"
        class="settings-switch"
        aria-labelledby={id}
        name={id}
        disabled={!usable}
        checked={usable && checked}
        onChange={(e) => onChange((e.target as HTMLInputElement).checked)}
      />
    </label>
  );
}

// styleHint says what the chosen way of saying answers does.
function styleHint(style: SpeakStyle): string {
  return choose(
    style === "natural",
    t(
      "Natural: the session's model says what the agent is doing while it works, and the gist of the answer at the end. Each line is a short extra call to the model.",
    ),
    t("Word for word: the answer as it is written."),
  );
}

// answersHint is the hint of a setting about answers read aloud: what it does, or, while answers
// are never read, that it waits for them.
function answersHint(mode: SpeakMode, hint: string): string {
  return choose(
    mode === "off",
    t("Needs answers read aloud: choose when above."),
    hint,
  );
}

// speaksNothing says nothing is said aloud at all: no answers, and no voice notices.
function speaksNothing(prefs: SpeechPrefs): boolean {
  return prefs.mode === "off" && !prefs.notices;
}

// rateHint is why the speed waits, or nothing while something is said aloud.
function rateHint(prefs: SpeechPrefs): string | undefined {
  return choose(
    speaksNothing(prefs),
    t("Needs answers read aloud or voice notices on."),
    undefined,
  );
}

// conversationHint says what conversation mode does, or what it waits on: speech input working
// here, and answers read aloud.
function conversationHint(canListen: boolean, mode: SpeakMode): string {
  if (!canListen) {
    return t("Needs speech input: turn it on in Voice input.");
  }
  if (mode === "off") {
    return t("Needs answers read aloud: choose when above.");
  }
  return t(
    "After reading the answer to a turn you spoke, Motita listens again and sends what you say. Say “that's all” or tap the microphone to end it.",
  );
}

// otherVoiceHint is why the voice for the other language cannot be picked yet, or "" when it can.
function otherVoiceHint(
  byLanguage: boolean,
  voices: number,
): string | undefined {
  if (!byLanguage) {
    return t("Needs a voice per language on.");
  }
  return choose(
    voices === 0,
    t("This device has no voice for that language."),
    undefined,
  );
}

// otherLang is the language an answer mixes in besides the interface's.
function otherLang(l: Lang): Lang {
  return choose(l === "es", "en", "es");
}

// LanguageVoice reads each sentence with a voice of its own language, and picks the voice of the
// other one: an answer in Spanish that quotes English is said by two voices, each in its own.
function LanguageVoice({
  off,
  prefs,
  set,
  allVoices,
}: {
  off: boolean;
  prefs: SpeechPrefs;
  set: (patch: Partial<SpeechPrefs>) => void;
  allVoices: VoiceLike[];
}) {
  const other = otherLang(lang());
  const voices = voicesFor(allVoices, other);
  const setByLanguage = useCallback(
    (byLanguage: boolean) => set({ byLanguage }),
    [set],
  );
  return (
    <>
      <Row
        id="settings-speech-bylang"
        label={t("A voice per language")}
        hint={t(
          "Each sentence is read with a voice of its own language: English with an English voice.",
        )}
      >
        <SpeechSwitch
          id="settings-speech-bylang"
          usable={!off}
          checked={prefs.byLanguage}
          onChange={setByLanguage}
        />
      </Row>
      <Row
        id="settings-speech-other-voice"
        label={choose(
          other === "en",
          t("Voice for English"),
          t("Voice for Spanish"),
        )}
        hint={otherVoiceHint(prefs.byLanguage, voices.length)}
      >
        <select
          class={selectCls}
          aria-labelledby="settings-speech-other-voice"
          disabled={off || !prefs.byLanguage}
          value={prefs.langVoices[other] || ""}
          onChange={(e) =>
            set({
              langVoices: {
                ...prefs.langVoices,
                [other]: (e.target as HTMLSelectElement).value,
              },
            })
          }
        >
          <option value="">{t("Device default")}</option>
          {voices.map((v) => (
            <option key={v.voiceURI} value={v.voiceURI}>
              {v.name}
            </option>
          ))}
        </select>
      </Row>
    </>
  );
}

// Pronunciations is the person's own list: a term and how to say it, one a line. It is saved when
// the field is left, so a half-typed line is never applied.
function Pronunciations({
  off,
  prefs,
  set,
}: {
  off: boolean;
  prefs: SpeechPrefs;
  set: (patch: Partial<SpeechPrefs>) => void;
}) {
  const saved = formatPronunciations(prefs.pronunciations);
  const [text, setText] = useState(saved);
  useEffect(() => setText(saved), [saved]);
  return (
    <Row
      id="settings-speech-words"
      label={t("Pronunciations")}
      stack
      hint={t(
        "One a line: the term, “=”, and how to say it. For example: kubectl = cube control",
      )}
    >
      <textarea
        class={inputCls + " w-full font-mono text-[12px]"}
        aria-labelledby="settings-speech-words"
        data-testid="speech-words"
        rows={3}
        disabled={off}
        spellcheck={false}
        value={text}
        onInput={(e) => setText((e.target as HTMLTextAreaElement).value)}
        onBlur={() => {
          const list = parsePronunciations(text);
          setText(formatPronunciations(list));
          if (formatPronunciations(list) !== saved) {
            set({ pronunciations: list });
          }
        }}
      />
    </Row>
  );
}
