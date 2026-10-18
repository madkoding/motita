// What each configured provider has consumed, in the sidebar's bottom menu: the tokens the provider
// reported since the gateway started, as a share of the total, drawn as bars or circular gauges. It shows no
// quota or balance, because no provider sends one with an answer; hovering a row gives the split
// and the calls. It stays quiet where the gateway has no such API.
import { useEffect, useState } from "preact/hooks";
import { USAGE_STYLE_EVENT, loadUsageStyle, type UsageStyle } from "./settings";
import { plural, t, tf } from "./i18n";
import { choose } from "./choose";
import { formatTokens, tokenBreakdown } from "./appFormat";
import {
  type Api,
  type ProviderUsage,
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
