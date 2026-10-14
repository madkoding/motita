// The provider picker of the model switcher: one button per provider the session can switch to,
// each with the provider's logo, so the choice reads at a glance instead of from a list of names.
import { t } from "./i18n";
import { choose } from "./choose";
import { PROVIDER_LOGOS } from "./providerLogos";
import type { ProviderInfo } from "./appTypes";

// ProviderLogo is the provider's logo, or its initial when there is none, on the settings tile.
export function ProviderLogo({ id, name }: { id: string; name: string }) {
  const logo = PROVIDER_LOGOS[id];
  return (
    <span class="git-account-icon llm-icon" aria-hidden="true" data-logo={id}>
      {logo ? (
        <svg width="18" height="18" viewBox="0 0 24 24" fill={logo.color}>
          <path d={logo.d} />
        </svg>
      ) : (
        name.charAt(0)
      )}
    </span>
  );
}

interface ProviderPickerProps {
  providers: ProviderInfo[];
  value: string;
  onPick: (id: string) => void;
}

// ProviderPicker lists the providers the gateway offers this session: those with a key (or a
// sign-in) plus the one it runs on now, so the active one is always there to go back to.
export function ProviderPicker({
  providers,
  value,
  onPick,
}: ProviderPickerProps) {
  return (
    <div class="provider-picker" role="radiogroup" aria-label={t("Provider")}>
      {providers.map((p) => (
        <button
          key={p.id}
          type="button"
          role="radio"
          aria-checked={value === p.id}
          title={choose(
            p.key_present,
            p.name,
            p.name + " — " + t("no key needed"),
          )}
          class={`provider-choice${choose(value === p.id, " is-active", "")}`}
          onClick={() => onPick(p.id)}
        >
          <ProviderLogo id={p.id} name={p.name} />
          <span class="provider-choice-name">{p.name}</span>
          {p.is_current && (
            <span class="provider-choice-current">{t("(active)")}</span>
          )}
        </button>
      ))}
    </div>
  );
}
