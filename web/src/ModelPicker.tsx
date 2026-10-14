import { useState } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { type LLMProvider, modelOptions } from "./llmApi";
import { inputCls } from "./ProviderSettings";

// The value of the option that opens the field for a model the list does not offer.
const OTHER = "\u0000other";

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
            {o.label}
          </option>
        ))}
        <option value={OTHER}>{t("Other…")}</option>
      </select>
      {typing && field}
    </>
  );
}
