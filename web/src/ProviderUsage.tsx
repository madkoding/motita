// What each configured provider has consumed, in the sidebar's bottom menu: the tokens the provider
// reported since the gateway started, as a share of the total, drawn as bars or circular gauges.
// Under them, the session and weekly quotas of the providers that report them (a ChatGPT plan, a
// Claude subscription), with the share used and when each starts over; the providers that report
// none are named as such, never guessed. Hovering a row gives the split and the calls. It stays
// quiet where the gateway has no such API.
import { useEffect, useState } from "preact/hooks";
import { USAGE_STYLE_EVENT, loadUsageStyle, type UsageStyle } from "./settings";
import { plural, t, tf } from "./i18n";
import { choose } from "./choose";
import { formatElapsed, formatTokens, tokenBreakdown } from "./appFormat";
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

// shareOf is a provider's share of everything the providers consumed, as a whole percentage.
export function shareOf(total: number, sum: number): number {
  return choose(sum > 0, Math.round((total / sum) * 100), 0);
}

// rowTitle is the tooltip of one provider: the split of its tokens and its calls.
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

// GAUGE_RADIUS and GAUGE_LENGTH size the circular gauge: the ring is a circle whose stroke is drawn
// for `share` percent of its circumference.
const GAUGE_RADIUS = 15;
const GAUGE_LENGTH = 2 * Math.PI * GAUGE_RADIUS;

// UsageGauge is one provider as a ring with its share in the middle and its name beneath.
function UsageGauge({ u, share }: { u: ProviderUsage; share: number }) {
  return (
    <li
      class="provider-usage-gauge"
      title={rowTitle(u)}
      data-testid={"provider-usage-" + u.id}
    >
      <svg viewBox="0 0 36 36" width="44" height="44" aria-hidden="true">
        <circle class="provider-usage-ring" cx="18" cy="18" r={GAUGE_RADIUS} />
        <circle
          class="provider-usage-arc"
          cx="18"
          cy="18"
          r={GAUGE_RADIUS}
          stroke-dasharray={`${(GAUGE_LENGTH * share) / 100} ${GAUGE_LENGTH}`}
        />
      </svg>
      <span class="provider-usage-pct tabular-nums">{share}%</span>
      <span class="provider-usage-name truncate">{u.name}</span>
    </li>
  );
}

// UsageRow is one provider: its name, its share and a bar filled to that share.
function UsageRow({ u, share }: { u: ProviderUsage; share: number }) {
  return (
    <li
      class="provider-usage-row"
      title={rowTitle(u)}
      data-testid={"provider-usage-" + u.id}
    >
      <span class="flex items-center gap-2 text-xs">
        <span class="flex-1 truncate text-[#e8e8ea]">{u.name}</span>
        <span class="tabular-nums text-[#9a9aaa]">{share}%</span>
      </span>
      <span class="provider-usage-bar" aria-hidden="true">
        <span class="provider-usage-fill" style={{ width: share + "%" }} />
      </span>
    </li>
  );
}

const MINUTES_PER_HOUR = 60;
const WARN_AT = 90;

// liveQuotas are the quotas a provider reported that have not started over since: a window whose
// reset has passed says nothing about now.
export function liveQuotas(u: ProviderUsage, now: number): UsageQuota[] {
  return (u.limits?.quotas ?? []).filter(
    (q) => !q.resets_at || Date.parse(q.resets_at) > now,
  );
}

// quotaLabel names a quota's window: "5 h" for a session of hours, "Week" for the weekly one.
export function quotaLabel(q: UsageQuota): string {
  if (q.name === "week") {
    return t("Week");
  }
  const m = q.window_minutes ?? 0;
  return choose(
    m > 0 && m % MINUTES_PER_HOUR === 0,
    tf("{n} h", { n: m / MINUTES_PER_HOUR }),
    t("Session"),
  );
}

// QuotaRow is one quota of one provider: its window, the share used and when it starts over.
function QuotaRow({
  u,
  q,
  now,
}: {
  u: ProviderUsage;
  q: UsageQuota;
  now: number;
}) {
  const used = Math.min(100, Math.max(0, Math.round(q.used_percent)));
  const reset = q.resets_at
    ? tf("resets in {t}", { t: formatElapsed(Date.parse(q.resets_at) - now) })
    : "";
  return (
    <li
      class="provider-quota"
      data-testid={`provider-quota-${u.id}-${q.name}`}
      title={reset}
    >
      <span class="flex items-center gap-2 text-[11px]">
        <span class="flex-1 truncate text-[#9a9aaa]">
          {u.name} · {quotaLabel(q)}
        </span>
        <span class="tabular-nums text-[#e8e8ea]">{used}%</span>
      </span>
      <span class="provider-usage-bar" aria-hidden="true">
        <span
          class={`provider-usage-fill${choose(used >= WARN_AT, " warn", "")}`}
          style={{ width: used + "%" }}
        />
      </span>
      {reset && <span class="text-[10px] text-[#7a7a8c]">{reset}</span>}
    </li>
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

// ProviderUsagePanel lists the providers by what they consumed, the busiest first.
export function ProviderUsagePanel({
  api,
  running,
}: {
  api: Api;
  running: boolean;
}) {
  const [rows, setRows] = useState<ProviderUsage[]>([]);
  const style = useUsageStyle();
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
  const sorted = [...rows].sort((a, b) => usageTotal(b) - usageTotal(a));
  const shown = sorted.slice(0, USAGE_ROWS);
  const sum = sorted.reduce((n, u) => n + usageTotal(u), 0);
  const more = sorted.length - shown.length;
  const now = Date.now();
  const quotas = shown.map((u) => ({ u, qs: liveQuotas(u, now) }));
  const silent = quotas.filter((x) => x.qs.length === 0).map((x) => x.u.name);
  return (
    <section
      class="px-4 py-2 border-t border-white/5 flex-none"
      aria-label={t("Provider usage")}
      data-testid="provider-usage"
    >
      <h2 class="text-[10px] font-semibold uppercase tracking-wider text-[#7a7a8c] pb-1">
        {t("Provider usage")}
      </h2>
      <ul
        class={choose(
          style === "gauge",
          "provider-usage-gauges",
          "space-y-1.5",
        )}
      >
        {shown.map((u) =>
          choose(
            style === "gauge",
            <UsageGauge key={u.id} u={u} share={shareOf(usageTotal(u), sum)} />,
            <UsageRow key={u.id} u={u} share={shareOf(usageTotal(u), sum)} />,
          ),
        )}
      </ul>
      {quotas.some((x) => x.qs.length > 0) && (
        <ul class="provider-quotas space-y-1.5" data-testid="provider-quotas">
          {quotas.flatMap((x) =>
            x.qs.map((q) => (
              <QuotaRow key={x.u.id + q.name} u={x.u} q={q} now={now} />
            )),
          )}
        </ul>
      )}
      {silent.length > 0 && (
        <p
          class="pt-1 text-[10px] text-[#7a7a8c]"
          data-testid="provider-usage-noquota"
        >
          {tf("No quota reported: {names}", { names: silent.join(", ") })}
        </p>
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
