// How much of each provider's plan is used, in the sidebar's bottom menu. Only what the provider
// itself reports is shown: the share of a quota (the 5-hour session, the week, the month) and when
// it starts over, or, for a Claude subscription before a threshold nears, only whether the usage is
// below its warning level. A provider that reports no quota says so, with the tokens it moved since
// the gateway started; nothing is guessed. Closed, the panel is each provider and its most used
// quota; open, every quota as a bar or a ring. It stays quiet where the gateway has no such API.
import { useCallback, useEffect, useState } from "preact/hooks";
import {
  USAGE_STYLE_EVENT,
  loadUsageOpen,
  loadUsageStyle,
  saveUsageOpen,
  type UsageStyle,
} from "./settings";
import { plural, t, tf } from "./i18n";
import { choose } from "./choose";
import { formatTokens, tokenBreakdown } from "./appFormat";
import {
  type Api,
  type ProviderUsage,
  type UsageQuota,
  getLLMUsage,
  usageTotal,
} from "./llmApi";

// How often the figures are read again while the page is open, and how many providers are named.
export const USAGE_POLL_MS = 60_000;
export const USAGE_ROWS = 4;

const MINUTE_MS = 60_000;
const MINUTES_PER_HOUR = 60;
const MINUTES_PER_DAY = 1440;
const WARN_AT = 90;

// usageTestId names a provider's row for the tests.
function usageTestId(u: ProviderUsage): string {
  return "provider-usage-" + u.id;
}

// rowTitle is the tooltip of one provider: its tokens, their split and its calls.
function rowTitle(u: ProviderUsage): string {
  const parts = [
    tf("{n} tokens", { n: formatTokens(usageTotal(u)) }),
    tokenBreakdown(u),
    plural(u.calls, "1 call", "{n} calls"),
  ];
  const w = u.limits?.tokens;
  if (w) {
    parts.push(
      tf("{left} of {max} tokens left in the rate window", {
        left: formatTokens(w.remaining),
        max: formatTokens(w.limit),
      }),
    );
  }
  return u.name + ": " + parts.join(" · ");
}

// liveQuotas are the quotas a provider reported that have not started over since: a window whose
// reset has passed says nothing about now.
export function liveQuotas(u: ProviderUsage, now: number): UsageQuota[] {
  return (u.limits?.quotas ?? []).filter(
    (q) => !q.resets_at || Date.parse(q.resets_at) > now,
  );
}

// quotaLabel names a quota's window: "5 h" for a session of hours, "Week" and "Month" for the longer ones.
export function quotaLabel(q: UsageQuota): string {
  if (q.name === "week") {
    return t("Week");
  }
  if (q.name === "month") {
    return t("Month");
  }
  const m = q.window_minutes ?? 0;
  return choose(
    m > 0 && m % MINUTES_PER_HOUR === 0,
    tf("{n} h", { n: m / MINUTES_PER_HOUR }),
    t("Session"),
  );
}

// usedOf is a quota's share used as a whole percentage between 0 and 100; a quota that states only
// where the usage stands counts as 0, so it never outranks a real share.
export function usedOf(q: UsageQuota): number {
  if (q.status) {
    return 0;
  }
  return Math.min(100, Math.max(0, Math.round(q.used_percent)));
}

// statusText is what a quota without a share says: below its warning level, or past it.
function statusText(q: UsageQuota): string {
  if (q.status === "warning") {
    return t("Near the limit");
  }
  return t("Below the warning level");
}

// rankOf is how far along a quota is, to order them: its share, or for one that states only where
// the usage stands, the warning level when it is past it and 0 below.
function rankOf(q: UsageQuota): number {
  if (q.status === "warning") {
    return WARN_AT;
  }
  return usedOf(q);
}

// peakQuota is a provider's most used quota, the one the closed panel shows.
export function peakQuota(qs: UsageQuota[]): UsageQuota | undefined {
  let peak: UsageQuota | undefined;
  for (const q of qs) {
    if (!peak || rankOf(q) > rankOf(peak)) {
      peak = q;
    }
  }
  return peak;
}

// formatReset is how long until a quota starts over, in days and hours, hours and minutes, or minutes.
export function formatReset(ms: number): string {
  const total = Math.max(1, Math.ceil(ms / MINUTE_MS));
  const days = Math.floor(total / MINUTES_PER_DAY);
  const hours = Math.floor((total % MINUTES_PER_DAY) / MINUTES_PER_HOUR);
  const minutes = total % MINUTES_PER_HOUR;
  if (days > 0 && hours > 0) {
    return tf("{d} d {h} h", { d: days, h: hours });
  }
  if (days > 0) {
    return tf("{d} d", { d: days });
  }
  if (hours > 0) {
    return tf("{h} h {m} min", { h: hours, m: minutes });
  }
  return tf("{m} min", { m: minutes });
}

// resetText says when a quota starts over, or "" when the provider did not say.
function resetText(q: UsageQuota, now: number): string {
  if (!q.resets_at) {
    return "";
  }
  return tf("resets in {t}", {
    t: formatReset(Date.parse(q.resets_at) - now),
  });
}

// ranked are the providers in the order the panel lists them: those with a quota first, the most
// used first, then the others by the tokens they moved.
function ranked(rows: ProviderUsage[], now: number) {
  const items = rows.map((u) => {
    const qs = liveQuotas(u, now);
    return { u, qs, peak: peakQuota(qs) };
  });
  const score = (x: (typeof items)[number]) => {
    if (!x.peak) {
      return -1;
    }
    return rankOf(x.peak);
  };
  return items.sort(
    (a, b) => score(b) - score(a) || usageTotal(b.u) - usageTotal(a.u),
  );
}

type Item = ReturnType<typeof ranked>[number];

// fillClass is the bar or ring of a share, amber once it nears the end.
function fillClass(base: string, used: number): string {
  return base + choose(used >= WARN_AT, " warn", "");
}

// QuotaBar is one quota as a line: its window, a bar and the share, then when it starts over.
function QuotaBar({ q, now }: { q: UsageQuota; now: number }) {
  const used = usedOf(q);
  const reset = resetText(q, now);
  return (
    <li class="provider-quota" data-testid={`provider-quota-${q.name}`}>
      <span class="provider-quota-line">
        <span class="provider-quota-label">{quotaLabel(q)}</span>
        {choose(
          q.status !== undefined,
          <span class={`provider-quota-status ${q.status}`}>
            {statusText(q)}
          </span>,
          <>
            <span class="provider-usage-bar" aria-hidden="true">
              <span
                class={fillClass("provider-usage-fill", used)}
                style={{ width: used + "%" }}
              />
            </span>
            <span class="provider-quota-pct tabular-nums">{used}%</span>
          </>,
        )}
      </span>
      {reset && <span class="provider-quota-reset">{reset}</span>}
    </li>
  );
}

// GAUGE_RADIUS and GAUGE_LENGTH size the ring: a circle whose stroke is drawn for the share used.
const GAUGE_RADIUS = 15;
const GAUGE_LENGTH = 2 * Math.PI * GAUGE_RADIUS;

// QuotaGauge is one quota as a ring with the share in the middle, its window and reset beneath.
// A quota that states only where the usage stands shows "OK", or "!" past its warning level.
function QuotaGauge({ q, now }: { q: UsageQuota; now: number }) {
  const used = usedOf(q);
  const reset = resetText(q, now);
  let middle = used + "%";
  let tip = reset;
  if (q.status) {
    middle = choose(q.status === "warning", "!", t("OK"));
    tip = [statusText(q), reset].filter(Boolean).join(" · ");
  }
  return (
    <li
      class="provider-usage-gauge"
      title={tip}
      data-testid={`provider-quota-${q.name}`}
    >
      <svg viewBox="0 0 36 36" width="44" height="44" aria-hidden="true">
        <circle class="provider-usage-ring" cx="18" cy="18" r={GAUGE_RADIUS} />
        {used > 0 && (
          <circle
            class={fillClass("provider-usage-arc", used)}
            cx="18"
            cy="18"
            r={GAUGE_RADIUS}
            stroke-dasharray={`${(GAUGE_LENGTH * used) / 100} ${GAUGE_LENGTH}`}
          />
        )}
      </svg>
      <span class={`provider-usage-pct tabular-nums ${q.status ?? ""}`}>
        {middle}
      </span>
      <span class="provider-usage-name">{quotaLabel(q)}</span>
      {q.resets_at && (
        <span class="provider-usage-name" data-testid="provider-gauge-reset">
          {formatReset(Date.parse(q.resets_at) - now)}
        </span>
      )}
    </li>
  );
}

// NoQuota is what a provider that reports no quota shows: that it reports none, and its tokens.
function NoQuota({ u }: { u: ProviderUsage }) {
  const total = usageTotal(u);
  const tokens = choose(
    total > 0,
    " · " + tf("{n} tokens", { n: formatTokens(total) }),
    "",
  );
  return (
    <p class="provider-quota-none" data-testid="provider-usage-noquota">
      {t("No quota reported") + tokens}
    </p>
  );
}

// ProviderBlock is one provider in the open panel: its name, then each quota or the note that it
// reports none.
function ProviderBlock({
  item,
  style,
  now,
}: {
  item: Item;
  style: UsageStyle;
  now: number;
}) {
  const { u, qs } = item;
  const Quota = choose(style === "gauge", QuotaGauge, QuotaBar);
  return (
    <li class="provider-block" data-testid={usageTestId(u)}>
      <span class="provider-block-name truncate" title={rowTitle(u)}>
        {u.name}
      </span>
      {choose(
        qs.length > 0,
        <ul
          class={choose(
            style === "gauge",
            "provider-usage-gauges",
            "provider-quotas",
          )}
        >
          {qs.map((q) => (
            <Quota key={q.name} q={q} now={now} />
          ))}
        </ul>,
        <NoQuota u={u} />,
      )}
    </li>
  );
}

// SummaryValue is what the closed panel says of a provider: its most used quota, or a dash.
function SummaryValue({ peak }: { peak?: UsageQuota }) {
  if (!peak) {
    return (
      <span class="provider-summary-value" title={t("No quota reported")}>
        —
      </span>
    );
  }
  const used = usedOf(peak);
  return (
    <span class="provider-summary-value">
      <span class="provider-summary-window">{quotaLabel(peak)}</span>
      {choose(
        peak.status !== undefined,
        <span class={`provider-quota-status ${peak.status}`}>
          {choose(peak.status === "warning", t("Near the limit"), t("OK"))}
        </span>,
        <span class={fillClass("tabular-nums", used)}>{used}%</span>,
      )}
    </span>
  );
}

// UsageSummary is the closed panel: each provider and its most used quota, as text.
function UsageSummary({ items }: { items: Item[] }) {
  return (
    <ul class="space-y-0.5">
      {items.map((x) => (
        <li
          key={x.u.id}
          class="provider-summary"
          title={rowTitle(x.u)}
          data-testid={usageTestId(x.u)}
        >
          <span class="flex-1 truncate">{x.u.name}</span>
          <SummaryValue peak={x.peak} />
        </li>
      ))}
    </ul>
  );
}

// useUsageStyle is the look chosen in Settings, kept in step when it changes.
function useUsageStyle(): UsageStyle {
  const [style, setStyle] = useState<UsageStyle>(loadUsageStyle);
  useEffect(() => {
    const sync = () => setStyle(loadUsageStyle());
    window.addEventListener(USAGE_STYLE_EVENT, sync);
    return () => window.removeEventListener(USAGE_STYLE_EVENT, sync);
  }, []);
  return style;
}

// useUsageOpen is whether the panel is open, closed until the user opens it.
function useUsageOpen(): [boolean, () => void] {
  const [open, setOpen] = useState(loadUsageOpen);
  const toggle = useCallback(() => {
    setOpen(!open);
    saveUsageOpen(!open);
  }, [open]);
  return [open, toggle];
}

// ProviderUsagePanel lists the providers, the most used plan first. It starts closed, with each
// provider and its most used quota; the title opens it.
export function ProviderUsagePanel({
  api,
  running,
}: {
  api: Api;
  running: boolean;
}) {
  const [rows, setRows] = useState<ProviderUsage[]>([]);
  const style = useUsageStyle();
  const [open, toggle] = useUsageOpen();
  useEffect(() => {
    let live = true;
    const load = () =>
      getLLMUsage(api)
        .then((v) => live && setRows(v ?? []))
        .catch(() => undefined);
    void load();
    // A run starting or ending changes the figures, so the effect also reads on those.
    const timer = setInterval(load, USAGE_POLL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [api, running]);
  if (rows.length === 0) {
    return null;
  }
  const now = Date.now();
  const all = ranked(rows, now);
  const shown = all.slice(0, USAGE_ROWS);
  const more = all.length - shown.length;
  return (
    <section
      class="px-4 py-2 border-t border-white/5 flex-none"
      aria-label={t("Provider usage")}
      data-testid="provider-usage"
    >
      <h2 class="pb-1">
        <button
          type="button"
          class="provider-usage-toggle"
          aria-expanded={open}
          onClick={toggle}
          data-testid="provider-usage-toggle"
        >
          <span aria-hidden="true">{choose(open, "▾", "▸")}</span>
          {t("Provider usage")}
        </button>
      </h2>
      {choose(
        open,
        <ul class="provider-blocks">
          {shown.map((x) => (
            <ProviderBlock key={x.u.id} item={x} style={style} now={now} />
          ))}
        </ul>,
        <UsageSummary items={shown} />,
      )}
      {more > 0 && (
        <p
          class="pt-1 text-[10px] text-[#7a7a8c]"
          data-testid="provider-usage-more"
        >
          {tf("+{n} more", { n: more })}
        </p>
      )}
    </section>
  );
}
