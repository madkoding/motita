import { useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { plural, t } from "./i18n";
import { choose } from "./choose";

// actionLabel is the counter shown while the list of actions is collapsed.
export function actionLabel(n: number): string {
  return plural(n, "1 action", "{n} actions");
}

// ActionCount is the counter of a turn's actions: a button that opens or closes their detail,
// wherever that detail is drawn.
export function ActionCount({
  count,
  open,
  onToggle,
}: {
  count: number;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      class="action-trail-toggle"
      aria-expanded={open}
      onClick={onToggle}
    >
      <span class="action-trail-caret" aria-hidden="true">
        {choose(open, "▾", "▸")}
      </span>
      <span class="action-trail-count">{actionLabel(count)}</span>
    </button>
  );
}

// ActionTrail shows the commands and actions of a turn as a counter. The detail is
// collapsed by default and opens or closes when the counter is clicked.
export function ActionTrail({
  count,
  children,
}: {
  count: number;
  children: ComponentChildren;
}) {
  const [open, setOpen] = useState(false);
  const toggle = () => setOpen((o) => !o);
  return (
    <div class="action-trail">
      <ActionCount count={count} open={open} onToggle={toggle} />
      {open && (
        <div class="activity-trail" aria-label={t("steps")}>
          {children}
        </div>
      )}
    </div>
  );
}
