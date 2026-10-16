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

// Chips say how capable a model is and what it costs, in words. They sit beside its name in the
// list and in the closed picker, so nothing about it is repeated elsewhere. A model the catalogue
// knows nothing about has none.
function Chips({ option }: { option: ModelOption }) {
  const tier = tierLabel(option.tier);
  const cost = costLabel(option.cost);
  return (
    <span class="llm-chips">
      {tier && (
        <span class="llm-chip llm-chip-tier" data-level={tierLevel(option.tier)}>
          {t(tier)}
        </span>
      )}
      {cost && (
        <span class="llm-chip llm-chip-cost" data-level={costLevel(option.cost)}>
          {t(cost)}
        </span>
      )}
    </span>
  );
}

// A step of the keyboard through the options: the index to land on, or -1 for a key that is not one.
function stepOf(key: string, at: number, last: number): number {
  const moves: Record<string, number> = {
    ArrowDown: Math.min(at + 1, last),
    ArrowUp: Math.max(at - 1, 0),
    Home: 0,
    End: last,
  };
  return moves[key] ?? -1;
}

// ModelListbox is a select that can draw each option with its chips, which a native one cannot.
function ModelListbox({
  id,
  ariaLabel,
  options,
  value,
  disabled,
  onPick,
}: {
  id: string;
  ariaLabel?: string;
  options: { id: string; label: string; option?: ModelOption }[];
  value: string;
  disabled: boolean;
  onPick: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const selected = Math.max(
    0,
    options.findIndex((o) => o.id === value),
  );
  const [active, setActive] = useState(selected);
  const toggle = () => {
    setActive(selected);
    setOpen((o) => !o);
  };
  const pick = (i: number) => {
    setOpen(false);
    onPick(options[i].id);
  };
  const onKey = (e: KeyboardEvent) => {
    const step = stepOf(e.key, active, options.length - 1);
    if (step >= 0) {
      e.preventDefault();
      setOpen(true);
      setActive(step);
      return;
    }
    if (e.key === "Escape") {
      setOpen(false);
      return;
    }
    if (e.key === "Enter" && open) {
      e.preventDefault();
      pick(active);
    }
  };
  const close = () => setOpen(false);
  const keepFocus = (e: MouseEvent) => e.preventDefault();
  const current = options[selected];
  return (
    <div class="llm-listbox">
      <button
        id={id}
        type="button"
        class={inputCls + " llm-listbox-trigger"}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={`${id}-list`}
        aria-label={ariaLabel}
        data-value={value}
        value={value}
        disabled={disabled}
        onClick={toggle}
        onKeyDown={onKey}
        onBlur={close}
      >
        <span class="llm-listbox-name">{current.label}</span>
        {current.option && <Chips option={current.option} />}
        <span class="llm-listbox-caret" aria-hidden="true">
          ▾
        </span>
      </button>
      {open && (
        <ul
          id={`${id}-list`}
          class="llm-listbox-list"
          role="listbox"
          aria-label={ariaLabel}
        >
          {options.map((o, i) => (
            <li
              key={o.id}
              id={`${id}-opt-${i}`}
              data-value={o.id}
              role="option"
              aria-selected={i === selected}
              class={choose(i === active, "active", "")}
              // A press must not blur the trigger first, or the list would close before it is read.
              onMouseDown={keepFocus}
              onClick={() => pick(i)}
              onKeyDown={onKey}
            >
              <span class="llm-listbox-name">{o.label}</span>
              {o.option && <Chips option={o.option} />}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// Price says, under the picker, what the chosen model roughly costs per million tokens.
function Price({ option }: { option: ModelOption | undefined }) {
  const price = option && priceText(option);
  if (!price) {
    return null;
  }
  return (
    <p class="text-xs text-[#7a7a8c]" data-testid="model-facts">
      {tf("About {price} per million tokens, read / written", { price })}
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
  const onPick = (v: string) => {
    setOther(v === OTHER);
    onChange(choose(v === OTHER, choose(offered, "", value), v));
  };
  const choices = [
    ...options.map((o) => ({ id: o.id, label: o.label, option: o })),
    { id: OTHER, label: t("Other…"), option: undefined },
  ];
  return (
    <>
      <ModelListbox
        id={id}
        ariaLabel={ariaLabel}
        options={choices}
        value={choose(typing, OTHER, value)}
        disabled={disabled}
        onPick={onPick}
      />
      {typing && field}
      {!typing && <Price option={options.find((o) => o.id === value)} />}
    </>
  );
}
