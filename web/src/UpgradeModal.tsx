// The upgrade modal: the version info and a confirm button, then a progress bar. While the upgrade
// runs, the modal cannot be dismissed.
import { t } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { upgradeMessage } from "./appParse";
import {
  IconAlert,
  IconCheck,
  IconClose,
  IconDownload,
  IconSpinner,
} from "./icons";
import type { UpgradeProgressEvent } from "./appTypes";

const SECONDARY =
  "min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform";

// ConfirmView is what the modal shows before the upgrade starts.
function ConfirmView() {
  const { updateInfo, runUpgrade, cancelUpgrade } = useApp().updates;
  return (
    <>
      <div class="space-y-3">
        <div class="flex items-center justify-between p-3 rounded-xl bg-black/20 border border-white/5">
          <div>
            <div class="text-xs text-[#7a7a8c] mb-0.5">
              {t("Current version")}
            </div>
            <div class="font-mono text-sm text-[#e8e8ea]">
              {updateInfo?.current_version || t("unknown")}
            </div>
          </div>
          <svg
            width="20"
            height="20"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            class="text-[#7a7a8c]"
          >
            <line x1="5" y1="12" x2="19" y2="12" />
            <polyline points="12 5 19 12 12 19" />
          </svg>
          <div class="text-right">
            <div class="text-xs text-[#7a7a8c] mb-0.5">
              {t("Latest version")}
            </div>
            <div class="font-mono text-sm text-accent">
              {updateInfo?.latest_version || t("unknown")}
            </div>
          </div>
        </div>
        {updateInfo?.release_name && (
          <div class="text-sm text-[#9a9aaa]">{updateInfo.release_name}</div>
        )}
        <p class="text-sm text-[#9a9aaa] leading-relaxed">
          {t(
            "This will download the new binary from GitHub, verify its checksum, replace the current executable, and",
          )}{" "}
          <strong class="text-[#e8e8ea]">{t("restart the gateway")}</strong>.{" "}
          {t("Any running tasks will be interrupted.")}
        </p>
        {updateInfo?.release_url && (
          <a
            href={updateInfo.release_url}
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-accent hover:underline"
          >
            {t("View release notes")} ↗
          </a>
        )}
      </div>
      <div class="flex gap-2 mt-5">
        <button
          class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform flex items-center justify-center gap-2"
          onClick={runUpgrade}
        >
          <IconDownload size={18} />
          {t("Download & Install")}
        </button>
        <button class={SECONDARY} onClick={cancelUpgrade}>
          {t("Cancel")}
        </button>
      </div>
    </>
  );
}

// stageLabel is the line under the progress bar: the stage, or the percentage while downloading.
function stageLabel(p: UpgradeProgressEvent): string {
  if (p.stage === "restarting") {
    return t("Restarting…");
  }
  if (p.stage === "done") {
    return t("Complete");
  }
  if (p.stage === "error") {
    return t("Failed");
  }
  return `${p.percent || 0}%`;
}

// ProgressBar is how far the upgrade went.
function ProgressBar({ p }: { p: UpgradeProgressEvent }) {
  const moving = p.stage === "downloading" || p.stage === "installing";
  return (
    <div class="relative">
      <div class="h-3 rounded-full bg-black/30 border border-white/5 overflow-hidden">
        <div
          class="h-full rounded-full transition-all duration-300 ease-out"
          style={{
            width: `${p.percent || 0}%`,
            background: choose(
              p.stage === "error",
              "linear-gradient(90deg, #ff6b6b, #ff8a8a)",
              "linear-gradient(90deg, #4cc2ff, #6dd5ff)",
            ),
            boxShadow: choose(moving, "0 0 12px rgba(76,194,255,0.4)", "none"),
          }}
        />
      </div>
      <div class="flex justify-between mt-1.5">
        <span class="text-xs text-[#9a9aaa]">{stageLabel(p)}</span>
        <span class="text-xs font-mono text-[#7a7a8c]">
          {p.version && `→ ${p.version}`}
        </span>
      </div>
    </div>
  );
}

// StageLine is the stage the upgrade is in, with its icon.
function StageLine({ p }: { p: UpgradeProgressEvent }) {
  const failed = p.stage === "error";
  const done = p.stage === "done";
  return (
    <div class="flex items-center gap-2 text-sm">
      {!failed && !done && (
        <IconSpinner size={16} class="animate-spin flex-none text-accent" />
      )}
      {done && <IconCheck size={16} class="text-accent flex-none" />}
      {failed && <IconAlert size={16} class="text-danger flex-none" />}
      <span class={choose(failed, "text-danger", "text-[#e8e8ea]")}>
        {upgradeMessage(p.message)}
      </span>
    </div>
  );
}

// UpgradeFailure has the same two-layer shape as the toast: a plain-language explanation the user
// can act on, with the raw text folded away behind "Details" for a bug report.
function UpgradeFailure({ error }: { error: string }) {
  const { showUpgradeError, toggleUpgradeError } = useApp().updates;
  return (
    <div class="p-3 rounded-xl bg-danger/10 border border-danger/20">
      <div class="flex items-start gap-2">
        <IconAlert size={15} class="text-danger flex-none mt-0.5" />
        <div class="flex-1 min-w-0">
          <p class="text-sm font-medium text-danger">
            {t("The upgrade did not finish")}
          </p>
          <p class="text-xs text-[#9a9aaa] mt-1 leading-relaxed">
            {t(
              "The gateway is still running the version it had before. Nothing was replaced, so you can keep using it and try again.",
            )}
          </p>
          <button
            class="text-xs text-[#7a7a8c] hover:text-[#9a9aaa] mt-1.5 underline underline-offset-2"
            onClick={toggleUpgradeError}
          >
            {choose(showUpgradeError, t("Hide details"), t("Details"))}
          </button>
          {showUpgradeError && (
            <pre class="text-xs text-danger whitespace-pre-wrap font-mono mt-2 p-2 rounded-lg bg-black/40">
              {error}
            </pre>
          )}
        </div>
      </div>
    </div>
  );
}

// ProgressView is the upgrade in progress, done or failed.
function ProgressView({ p }: { p: UpgradeProgressEvent }) {
  const { upgradeBusy, upgradeError, finishUpgrade } = useApp().updates;
  return (
    <>
      <div class="space-y-4">
        <ProgressBar p={p} />
        <StageLine p={p} />
        {upgradeError && <UpgradeFailure error={upgradeError} />}
        {p.stage === "restarting" && (
          <p class="text-xs text-[#7a7a8c] leading-relaxed">
            {t(
              "The gateway is restarting. This page will reload automatically once it is back.",
            )}
          </p>
        )}
        {p.stage === "done" && (
          <p class="text-xs text-[#7a7a8c] leading-relaxed">
            {t(
              "The upgrade is complete and the gateway is restarting. This page will reload automatically in a moment.",
            )}
          </p>
        )}
      </div>
      {!upgradeBusy && p.stage !== "restarting" && (
        <div class="flex gap-2 mt-5">
          <button class={"flex-1 " + SECONDARY} onClick={finishUpgrade}>
            {choose(p.stage === "error", t("Close"), t("Done"))}
          </button>
        </div>
      )}
    </>
  );
}

export function UpgradeModal() {
  const { upgradeBusy, upgradeProgress, closeUpgradeIfIdle } = useApp().updates;
  return (
    <ModalBackdrop onClose={closeUpgradeIfIdle}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl">
        <div class="flex items-center gap-2 mb-4">
          <IconDownload size={20} class="text-accent" />
          <h2 class="text-base font-semibold">{t("Upgrade Motita")}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeUpgradeIfIdle}
            disabled={upgradeBusy}
            aria-label={t("Close")}
            style={choose(upgradeBusy, "opacity:0.3;cursor:not-allowed", "")}
          >
            <IconClose size={18} />
          </button>
        </div>
        {!upgradeBusy && !upgradeProgress && <ConfirmView />}
        {upgradeProgress && <ProgressView p={upgradeProgress} />}
      </div>
    </ModalBackdrop>
  );
}
