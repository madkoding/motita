// The toast: an auto-dismissing notification at the bottom of the screen.
//
// Centred with flex on a full-width wrapper rather than with translate-x-1/2: the slideUp keyframe
// animates `transform`, and a transform on the card would OVERRIDE the translate that centres it,
// which pushed the card off the right edge of a narrow window. `detail` is the second line: a
// merge names the commit it produced, and a failure says what did NOT change, which is the part a
// user actually needs. When the toast mentions a new version, it offers the upgrade modal.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { toastIcon, toastTone } from "./appFormat";
import { IconAlert, IconCheck, IconClose, IconDownload } from "./icons";
import type { Toast } from "./appTypes";

// ToastIcon is the icon of the toast's kind. A warning has a triangle, not the error's circle: the
// two are different answers ("something is wrong but recovering" vs "this failed"), and a shared
// icon would make the reader parse the sentence to tell them apart.
function ToastIcon({ kind }: { kind: string }) {
  if (kind === "error") {
    return <IconAlert size={20} class="text-danger flex-none mt-0.5" />;
  }
  if (kind === "warning") {
    return (
      <svg
        width="20"
        height="20"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        class="text-[#edc06a] flex-none mt-0.5"
      >
        <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
        <line x1="12" y1="9" x2="12" y2="13" />
        <line x1="12" y1="17" x2="12.01" y2="17" />
      </svg>
    );
  }
  if (kind === "update") {
    return (
      <IconDownload
        size={20}
        class="text-accent flex-none mt-0.5 animate-pulse"
      />
    );
  }
  return <IconCheck size={20} class="text-accent flex-none mt-0.5" />;
}

// ToastDetails is the technical text of a failure, folded behind "Details".
function ToastDetails({ technical }: { technical: string }) {
  const { toastDetailsOpen, toggleToastDetails } = useApp().status;
  return (
    <div class="mt-2">
      <button
        class="text-[11px] text-[#7a7a8c] hover:text-[#9a9aaa] underline decoration-dotted"
        onClick={(e) => {
          e.stopPropagation();
          toggleToastDetails();
        }}
      >
        {choose(toastDetailsOpen, t("Hide details"), t("Details"))}
      </button>
      {toastDetailsOpen && (
        <pre class="mt-1.5 text-[11px] text-[#8a8a9a] font-mono bg-black/30 rounded-lg p-2 overflow-x-auto whitespace-pre-wrap wrap-break-word max-h-32 overflow-y-auto">
          {technical}
        </pre>
      )}
    </div>
  );
}

export function ToastCard({ toast }: { toast: Toast }) {
  const { status, updates } = useApp();
  const { updateInfo, openUpgrade, dismissUpgradeToast } = updates;
  const offersUpgrade = !!updateInfo?.update_available && !!toast.update;
  return (
    <div class="fixed bottom-[calc(1.5rem+env(safe-area-inset-bottom))] inset-x-0 z-60 flex justify-center px-4 pointer-events-none">
      <div
        class="frosted rounded-xl border px-4 py-3 flex items-start gap-3 max-w-md w-full pointer-events-auto"
        style={`animation: slideUp 0.3s ease-out; box-shadow: 0 20px 45px -10px rgba(0,0,0,0.75); border-color: ${toastTone(toast.type, "rgba(239,68,68,0.35)", "rgba(237,181,88,0.35)", "rgba(76,194,255,0.35)")}`}
      >
        <ToastIcon kind={toastIcon(toast.type, toast.update, updateInfo)} />
        <div class="flex-1 min-w-0">
          <p
            class={`text-sm font-semibold ${toastTone(toast.type, "text-danger", "text-[#edc06a]", "text-[#e8e8ea]")}`}
          >
            {toast.message}
          </p>
          {toast.detail && (
            <p class="text-xs text-[#9a9aaa] mt-1 leading-relaxed wrap-break-word">
              {toast.detail}
            </p>
          )}
          {toast.technical && <ToastDetails technical={toast.technical} />}
          {offersUpgrade && (
            <button
              class="mt-2 text-xs font-semibold text-accent hover:underline"
              onClick={(e) => {
                e.stopPropagation();
                openUpgrade();
              }}
            >
              {t("Upgrade now")}
            </button>
          )}
        </div>
        <button
          class="flex-none p-1 rounded-lg hover:bg-white/10 text-[#7a7a8c]"
          onClick={(e) => {
            e.stopPropagation();
            // A version toast is remembered as dismissed, so it does not come back on the next
            // poll; any other toast just goes away.
            if (offersUpgrade) {
              dismissUpgradeToast();
            } else {
              status.dismissToast();
            }
          }}
          aria-label={t("Dismiss")}
        >
          <IconClose size={14} />
        </button>
      </div>
    </div>
  );
}
