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
  RATES,
  SHORT_CHARS,
  type SpeakMode,
  type SpeakStyle,
  type SpeechEngine,
  type SpeechPrefs,
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
): { value: SpeechEngine; label: string }[] {
  const engines: { value: SpeechEngine; label: string }[] = [];
  if (device) {
    engines.push({ value: "device", label: t("This device") });
  }
  if (hasGateway) {
    engines.push({ value: "gateway", label: t("The gateway") });
  }
  if (hasLocal) {
    engines.push({ value: "local", label: t("Local voice") });
  }
  return engines;
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

// engineShownOf is the voice that speaks, and what the switch says about it.
function engineShownOf(
  localChosen: boolean,
  gateway: boolean,
): { engineShown: SpeechEngine; engineHint: string } {
  if (localChosen) {
    return {
      engineShown: "local",
      engineHint: t(
        "A voice that runs in this browser: the gateway downloads it once, and after that nothing leaves the device.",
      ),
    };
  }
  if (gateway) {
    return {
      engineShown: "gateway",
      engineHint: t(
        "The gateway's voice: the same on every device. The text of each answer read goes to the speech service.",
      ),
    };
  }
  return {
    engineShown: "device",
    engineHint: t("The voices of this device: nothing leaves the browser."),
  };
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
  const { engineShown, engineHint } = engineShownOf(localChosen, gateway);
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
          hint={choose(
            prefs.style === "natural",
            t(
              "Natural: the session's model says what the agent is doing while it works, and the gist of the answer at the end. Each line is a short extra call to the model.",
            ),
            t("Word for word: the answer as it is written."),
          )}
        >
          <Segmented<SpeakStyle>
            name="settings-speak-style"
            labelledBy="settings-speak-style"
            value={prefs.style}
            disabled={off}
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
            hint={engineHint}
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
        <Row id="settings-speech-rate" label={t("Speed")} stack>
          <Segmented<string>
            name="settings-speech-rate"
            labelledBy="settings-speech-rate"
            value={String(prefs.rate)}
            disabled={off}
            onChange={setRate}
            options={RATES.map((r) => ({
              value: String(r),
              label: rateLabel(r),
            }))}
          />
        </Row>
        <Row
          id="settings-speech-length"
          label={t("Length")}
          stack
          hint={t("A longer answer is read up to its first paragraph.")}
        >
          <Segmented<string>
            name="settings-speech-length"
            labelledBy="settings-speech-length"
            value={String(prefs.maxChars)}
            disabled={off}
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
          hint={choose(
            canListen,
            t(
              "After reading the answer to a turn you spoke, Motita listens again and sends what you say. Say “that's all” or tap the microphone to end it.",
            ),
            t("Needs speech input: turn it on above."),
          )}
        >
          <SpeechSwitch
            id="settings-speech-conversation"
            usable={!off && canListen}
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
      <Row id="settings-speech-local" label={t("Local voices")} stack>
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
      {prefs.byLanguage && !off && (
        <Row
          id="settings-speech-other-voice"
          label={choose(
            other === "en",
            t("Voice for English"),
            t("Voice for Spanish"),
          )}
          hint={choose(
            voices.length === 0,
            t("This device has no voice for that language."),
            undefined,
          )}
        >
          <select
            class={selectCls}
            aria-labelledby="settings-speech-other-voice"
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
      )}
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
