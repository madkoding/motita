import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type LLMProvider,
  type ModelOption,
  costLabel,
  costLevel,
  modelOptions,
  priceText,
  tierLabel,
  tierLevel,
} from "./llmApi";
import { inputCls } from "./ProviderSettings";

// The value of the option that opens the field for a model the list does not offer.
const OTHER = "\u0000other";

// optionText is a model's name followed by how capable it is and what it costs, in words, so two
// can be compared in the list itself. A model the catalogue knows nothing about is its name alone.
function optionText(o: ModelOption): string {
  const words = [tierLabel(o.tier), costLabel(o.cost)].filter(Boolean);
  return [o.label, ...words.map((w) => t(w))].join(" · ");
}

// Meter is a label, three dots filled up to a level, and the word for it.
function Meter({
  name,
  word,
  level,
}: {
  name: string;
  word: string;
  level: number;
}) {
  return (
    <div class="llm-meter" role="img" aria-label={`${name}: ${word}`}>
      <span class="llm-meter-name">{name}</span>
      <span class="llm-meter-dots" aria-hidden="true">
        {[1, 2, 3].map((n) => (
          <i key={n} class={choose(n <= level, "on", "")} />
        ))}
      </span>
      <span class="llm-meter-word">{word}</span>
    </div>
  );
}

// Facts says, under the picker, how capable the chosen model is and what it costs.
function Facts({ option }: { option: ModelOption | undefined }) {
  const tier = tierLabel(option?.tier);
  const cost = costLabel(option?.cost);
  if (!option || (!tier && !cost)) {
    return null;
  }
  const price = priceText(option);
  return (
    <div class="llm-model-facts" data-testid="model-facts">
      {tier && (
        <Meter
          name={t("Capability")}
          word={t(tier)}
          level={tierLevel(option.tier)}
        />
      )}
      {cost && (
        <Meter
          name={t("Cost")}
          word={t(cost)}
          level={costLevel(option.cost)}
        />
      )}
      {price && (
        <p class="text-xs text-[#7a7a8c]">
          {tf("About {price} per million tokens, read / written", { price })}
        </p>
      )}
    </div>
  );
}

// ModelPicker picks a model from the list the provider offers. "Other…" opens a field for one the
// list leaves out; a provider that offers none gets the field alone.
export function ModelPicker({
  id,
  provider,
  value,
  disabled,
  ariaLabel,
  onChange,
}: {
  id: string;
  provider: LLMProvider | undefined;
  value: string;
  disabled: boolean;
  ariaLabel?: string;
  onChange: (model: string) => void;
}) {
  const options = modelOptions(provider);
  const offered = options.some((o) => o.id === value);
  const [other, setOther] = useState(false);
  const typing = options.length === 0 || other || (value !== "" && !offered);
  const field = (
    <input
      id={choose(options.length === 0, id, `${id}-other`)}
      class={inputCls + " font-mono"}
      value={value}
      placeholder={t("Model name, as the provider writes it")}
      aria-label={ariaLabel}
      disabled={disabled}
      autocapitalize="off"
      spellcheck={false}
      onInput={(e) => onChange((e.target as HTMLInputElement).value)}
    />
  );
  if (options.length === 0) {
    return field;
  }
  return (
    <>
      <select
        id={id}
        class={inputCls}
        value={choose(typing, OTHER, value)}
        aria-label={ariaLabel}
        disabled={disabled}
        onChange={(e) => {
          const v = (e.target as HTMLSelectElement).value;
          setOther(v === OTHER);
          onChange(choose(v === OTHER, choose(offered, "", value), v));
        }}
      >
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {optionText(o)}
          </option>
        ))}
        <option value={OTHER}>{t("Other…")}</option>
      </select>
      {typing && field}
      {!typing && <Facts option={options.find((o) => o.id === value)} />}
    </>
  );
}
