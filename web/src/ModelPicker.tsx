import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import {
  type LLMProvider,
  type ModelOption,
  costMark,
  modelOptions,
  priceText,
  tierName,
} from "./llmApi";
import { inputCls } from "./ProviderSettings";

// The value of the option that opens the field for a model the list does not offer.
const OTHER = "\u0000other";

// optionText is a model's name followed by its cost and capability badges, so two can be compared
// in the list itself. A model the catalogue knows nothing about is its name alone.
function optionText(o: ModelOption): string {
  const badges = [costMark(o.cost), tierName(o.tier) && t(tierName(o.tier))];
  return [o.label, ...badges.filter(Boolean)].join(" · ");
}

// Facts says, under the picker, what the chosen model costs and how capable it is.
function Facts({ option }: { option: ModelOption | undefined }) {
  const tier = tierName(option?.tier);
  const mark = costMark(option?.cost);
  if (!option || (!tier && !mark)) {
    return null;
  }
  const price = priceText(option);
  const parts = [
    mark && tf("Cost: {cost}", { cost: mark }),
    tier && tf("Capability: {tier}", { tier: t(tier) }),
    price && tf("{price} per million tokens, in / out (approximate)", { price }),
  ].filter(Boolean);
  return (
    <p class="text-xs text-[#7a7a8c] llm-model-facts" data-testid="model-facts">
      {parts.join(" · ")}
    </p>
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
      {typing ? field : <Facts option={options.find((o) => o.id === value)} />}
    </>
  );
}
