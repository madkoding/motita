import type { ComponentChildren } from "preact";
import { choose } from "./choose";

// The classes the settings sections share: a field, a small button, and its two emphasised kinds.
export const inputCls =
  "min-w-0 px-2 py-1.5 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-hidden focus:border-accent";
export const smallBtn =
  "btn-outline px-3 min-h-[32px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform";
export const primaryBtn = smallBtn + " bg-accent text-white border-transparent";
export const dangerBtn = smallBtn + " text-danger";

export interface Choice<T extends string> {
  value: T;
  label: string;
}

// Segmented draws a short list of choices as one row of radio buttons: every option is in sight
// and a finger picks it in one tap, where a <select> takes two and hides the others.
export function Segmented<T extends string>({
  name,
  labelledBy,
  value,
  options,
  onChange,
  disabled,
}: {
  name: string;
  labelledBy: string;
  value: T;
  options: Choice<T>[];
  onChange: (v: T) => void;
  disabled?: boolean;
}) {
  return (
    <div role="radiogroup" aria-labelledby={labelledBy} class="settings-seg">
      {options.map((o) => (
        <label key={o.value} class="settings-seg-opt">
          <input
            type="radio"
            class="sr-only"
            name={name}
            value={o.value}
            checked={value === o.value}
            disabled={disabled}
            onChange={() => onChange(o.value)}
          />
          {o.label}
        </label>
      ))}
    </div>
  );
}

// Row is one setting: its name (and a hint or a warning under it) on one side and its control on
// the other. On a narrow phone the control goes under the name, full width (see .settings-row in
// index.css).
export function Row({
  id,
  label,
  hint,
  stack,
  warning,
  children,
}: {
  id: string;
  label: string;
  hint?: ComponentChildren;
  // stack keeps the control under the name at every width, for a control too wide to share it.
  stack?: boolean;
  warning?: ComponentChildren;
  children: ComponentChildren;
}) {
  return (
    <div class={"settings-row" + choose(stack, " settings-row-stack", "")}>
      <div class="settings-row-text">
        <div id={id} class="text-sm text-[#e8e8ea]">
          {label}
        </div>
        {hint && <div class="text-xs text-[#7a7a8c] mt-0.5">{hint}</div>}
        {warning}
      </div>
      <div class="settings-row-control">{children}</div>
    </div>
  );
}
