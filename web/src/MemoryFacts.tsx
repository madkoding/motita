// What an entry says about itself, in plain words: what kind of memory it is, who wrote it and when,
// the project and repositories it belongs to, and how it is tied to other entries ("Replaces…",
// "Related to…"). The front matter these come from is never shown as text.
import { t } from "./i18n";
import { useApp } from "./appStore";
import {
  authorName,
  groupRelations,
  KINDS,
  kindLabel,
  relationLabel,
} from "./memoryKinds";
import type { EntryMeta, EntryView } from "./memoryApi";

// factsLine says what an entry is, who wrote it and when, and where it belongs, in one line.
export function factsLine(meta: Partial<EntryMeta> | undefined): string {
  const m = meta ?? {};
  return [
    m.type && t(kindLabel(m.type)),
    authorName(m.author),
    (m.date ?? "").slice(0, 10),
    m.project ?? "",
    (m.repos ?? []).join(", "),
  ]
    .filter(Boolean)
    .join(" · ");
}

// Chip is one associated entry: a button that opens it, or its name alone when it is not written yet.
function Chip({
  item,
  onFollow,
}: {
  item: { target: string; path?: string };
  onFollow: (path: string) => void;
}) {
  if (!item.path) {
    return (
      <span
        class="text-xs text-[#7a7a8c]"
        title={t("No entry with this name yet")}
      >
        {item.target}
      </span>
    );
  }
  const path = item.path;
  const follow = () => onFollow(path);
  return (
    <button
      type="button"
      class="memory-chip"
      data-testid="association"
      onClick={follow}
    >
      {item.target}
    </button>
  );
}

// Associations are the entries the open one is tied to, grouped by how: each one that exists opens
// with onFollow, one not written yet is named as it is.
export function Associations({
  entry,
  onFollow,
}: {
  entry: EntryView;
  onFollow: (path: string) => void;
}) {
  const groups = groupRelations(entry.meta?.relations, entry.links);
  if (groups.length === 0) {
    return null;
  }
  return (
    <section
      class="mt-3 pt-3 border-t border-white/10 text-sm"
      aria-label={t("Associated with")}
      data-testid="entry-associations"
    >
      <h3 class="text-xs text-[#9a9aaa] mb-1.5">{t("Associated with")}</h3>
      <ul class="space-y-1">
        {groups.map((g) => (
          <li key={g.kind} class="flex flex-wrap items-baseline gap-1.5">
            <span class="text-xs text-[#9a9aaa]">
              {t(relationLabel(g.kind))}
            </span>
            {g.items.map((it) => (
              <Chip key={it.target} item={it} onFollow={onFollow} />
            ))}
          </li>
        ))}
      </ul>
    </section>
  );
}

// KindFilter narrows a search to one kind of memory.
export function KindFilter({ class: cls }: { class: string }) {
  const { filters, filter } = useApp().memory;
  return (
    <select
      class={cls}
      value={filters.type}
      aria-label={t("Kind of memory")}
      data-testid="filter-type"
      onChange={(e) =>
        void filter({ type: (e.target as HTMLSelectElement).value })
      }
    >
      <option value="">{t("Every kind")}</option>
      {KINDS.map((k) => (
        <option key={k} value={k}>
          {t(kindLabel(k))}
        </option>
      ))}
    </select>
  );
}
