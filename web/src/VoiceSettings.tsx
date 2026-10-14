import { useCallback } from "preact/hooks";
import { InsecureWarning } from "./InsecureWarning";
import { lang, t, tf } from "./i18n";
import { choose } from "./choose";
import type { VoicePrefs, VoiceState } from "./voice";
import {
  LENGTHS,
  RATES,
  type SpeakMode,
  type SpeakStyle,
  type SpeechPrefs,
  type VoiceLike,
} from "./speech";
import { Row, Segmented, inputCls, smallBtn } from "./SettingsControls";

export interface VoiceSettingsProps {
  state: VoiceState;
  hands: VoiceState | "no-phrases";
  phrases: string[];
  prefs: VoicePrefs;
  setPrefs: (p: VoicePrefs) => void;
}

export interface SpeechSettingsProps {
  supported: boolean;
  prefs: SpeechPrefs;
  setPrefs: (p: SpeechPrefs) => void;
  voices: VoiceLike[];
  test: () => void;
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
  if (n <= 300) {
    return t("Short");
  }
  if (n <= 600) {
    return t("Medium");
  }
  return t("Long");
}

// SpeechSettings is reading answers aloud with the voices of this device: when, which voice, how
// fast and how much. It needs neither the microphone nor the gateway, so it works on any address.
export function SpeechSettings({
  supported,
  prefs,
  setPrefs,
  voices,
  test,
}: SpeechSettingsProps) {
  const set = useCallback(
    (patch: Partial<SpeechPrefs>) => setPrefs({ ...prefs, ...patch }),
    [prefs, setPrefs],
  );
  // Handed to Segmented, so they keep their identity between renders.
  const setMode = useCallback((mode: SpeakMode) => set({ mode }), [set]);
  const setStyle = useCallback((style: SpeakStyle) => set({ style }), [set]);
  const setRate = useCallback((r: string) => set({ rate: Number(r) }), [set]);
  const setLength = useCallback(
    (n: string) => set({ maxChars: Number(n) }),
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
            t(
              "With the voices of this device. Alt+L or 🔊 on an answer reads it or stops.",
            ),
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
            class={inputCls + " max-w-[220px]"}
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

// voiceBlocker explains why speech input cannot be used, or "" when it can. An insecure page is
// not one of them: that one is the InsecureWarning pill (see voiceWarning).
function voiceBlocker(state: string): string {
  if (state === "unsupported") {
    return t("This browser cannot record audio.");
  }
  if (state === "off") {
    return t("Off on the gateway: set voice.enabled in the configuration.");
  }
  if (state === "no-phrases") {
    return t("Add voice.wake_phrases to the configuration first.");
  }
  return "";
}

// The guide section that says how to give the page a secure address for the microphone.
const VOICE_GUIDE =
  "https://github.com/madkoding/motita/blob/main/docs/REFERENCE.md#voice-input";

// voiceWarning is the pill for a voice switch the browser hides on an insecure page.
function voiceWarning(state: string) {
  if (state !== "insecure") {
    return undefined;
  }
  return (
    <InsecureWarning
      text={t(
        "Your browser blocks the microphone because Motita was opened without a secure connection.",
      )}
      guide={VOICE_GUIDE}
    />
  );
}

// voiceHint is the hint under a voice switch: none on an insecure page (the pill says it), why
// the switch cannot be used, or what it does.
function voiceHint(state: string, usable: string): string | undefined {
  if (state === "insecure") {
    return undefined;
  }
  return voiceBlocker(state) || usable;
}

// VoiceSwitch is one switch of the speech section, greyed out while it cannot be used.
function VoiceSwitch({
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
        disabled={!usable}
        checked={usable && checked}
        onChange={(e) => onChange((e.target as HTMLInputElement).checked)}
      />
    </label>
  );
}

// VoiceSettings are the microphone and the wake phrase, which only work where the browser grants
// the microphone (https or localhost) and the gateway has speech input on.
export function VoiceSettings({
  state,
  hands,
  phrases,
  prefs,
  setPrefs,
}: VoiceSettingsProps) {
  const setMic = useCallback(
    (mic: boolean) => setPrefs({ ...prefs, mic }),
    [prefs, setPrefs],
  );
  const setListen = useCallback(
    (listen: boolean) => setPrefs({ ...prefs, listen }),
    [prefs, setPrefs],
  );
  return (
    <section class="settings-group" aria-labelledby="settings-voice-h">
      <h3 id="settings-voice-h" class="settings-group-title">
        {t("Voice")}
      </h3>
      <div class="settings-card" data-testid="voice-setting">
        <Row
          id="settings-mic"
          label={t("Microphone button")}
          warning={voiceWarning(state)}
          hint={voiceHint(
            state,
            t(
              "Dictate into the message box. You read and edit the text before sending it.",
            ),
          )}
        >
          <VoiceSwitch
            id="settings-mic"
            usable={state === "ready"}
            checked={prefs.mic}
            onChange={setMic}
          />
        </Row>
        <Row
          id="settings-listen"
          label={t("Listen for the wake phrase")}
          warning={voiceWarning(hands)}
          hint={voiceHint(
            hands,
            tf(
              "Only while this tab is open. What you say is sent to the transcription service. Say: {phrases}",
              { phrases: phrases.map((p) => `“${p}”`).join(", ") },
            ),
          )}
        >
          <VoiceSwitch
            id="settings-listen"
            usable={hands === "ready"}
            checked={prefs.listen}
            onChange={setListen}
          />
        </Row>
      </div>
    </section>
  );
}
