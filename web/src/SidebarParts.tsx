// The pieces the sidebar's sections are drawn with: a section's title, the "+" that adds to it, and
// the line that says it is empty.
import type { ComponentChildren } from "preact";
import { buttonIcon } from "./NewSessionButton";

// SectionAdd is the small "+" at the right of a section title: it adds one more of what the
// section lists. While a session is being created it shows the spinner and cannot be repeated.
export function SectionAdd({
  label,
  onClick,
  loading = false,
  testid,
}: {
  label: string;
  onClick: () => void;
  loading?: boolean;
  testid: string;
}) {
  return (
    <button
      type="button"
      class="section-add flex items-center justify-center p-1 rounded-md text-[#7a7a8c] hover:text-accent hover:bg-white/5 active:scale-95 transition-all disabled:opacity-60 disabled:cursor-wait"
      onClick={onClick}
      disabled={loading}
      aria-label={label}
      title={label}
      data-testid={testid}
    >
      {buttonIcon(loading)}
    </button>
  );
}

// SectionTitle is the label over one part of the list, so a session and a project are never
// mistaken for each other, with the "+" that adds to that part.
export function SectionTitle({
  id,
  label,
  children,
}: {
  id: string;
  label: string;
  children: ComponentChildren;
}) {
  return (
    <div class="flex items-center gap-2 pl-2 pr-1 pt-3 pb-1">
      <h2
        id={id}
        class="flex-1 text-[10px] font-semibold uppercase tracking-wider text-[#7a7a8c]"
      >
        {label}
      </h2>
      {children}
    </div>
  );
}

// EmptySection says a part of the list has nothing in it, rather than leaving a blank gap.
export function EmptySection({ label }: { label: string }) {
  return (
    <div
      class="mx-1 px-3 py-2 rounded-xl border border-dashed border-white/6 text-xs text-[#7a7a8c] italic"
      data-testid="sidebar-empty"
    >
      {label}
    </div>
  );
}
