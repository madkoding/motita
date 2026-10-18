// The dialog that connects another computer: its token, and its address when it was not found on
// the network (one across a VPN). The token is sent to THIS gateway, which checks it with the
// other computer and keeps it; the page never holds it.
import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useMachinesCtx, type ConnectTarget } from "./useMachines";
import { ModalBackdrop } from "./ModalBackdrop";

const FIELD =
  "w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent";
const LABEL = "block text-sm text-[#9a9aaa] mb-1.5";

export function ConnectMachineModal() {
  const { connecting, connect, closeConnect } = useMachinesCtx();
  const machine = (connecting as ConnectTarget).machine;
  const [address, setAddress] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const title = machine
    ? tf("Connect {name}", { name: machine.name })
    : t("Add a computer");
  const incomplete = !token.trim() || (!machine && !address.trim());
  return (
    <ModalBackdrop onClose={closeConnect}>
      <form
        class="frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl"
        data-testid="connect-machine"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(await connect(token.trim(), address.trim()));
          setBusy(false);
        }}
      >
        <h2 class="text-base font-semibold mb-1">{title}</h2>
        <p class="text-sm text-[#9a9aaa] mb-4">
          {t(
            "Type that computer's gateway token. On that computer, `motita gateway start -token-only` shows it. Only computers on the local network, a VPN or the tailnet can be connected.",
          )}
        </p>
        <div class="space-y-4">
          {!machine && (
            <div>
              <label class={LABEL} for="machine-address">
                {t("Address")}
              </label>
              <input
                id="machine-address"
                class={FIELD}
                value={address}
                onInput={(e) =>
                  setAddress((e.target as HTMLInputElement).value)
                }
                placeholder="fd7a:115c:a1e0::2 · 192.168.1.20:7477"
                autoFocus
              />
            </div>
          )}
          <div>
            <label class={LABEL} for="machine-token">
              {t("Token")}
            </label>
            <input
              id="machine-token"
              type="password"
              autoComplete="off"
              class={FIELD}
              value={token}
              onInput={(e) => setToken((e.target as HTMLInputElement).value)}
              autoFocus={!!machine}
            />
          </div>
          {error && (
            <p class="text-sm text-danger" role="alert">
              {error}
            </p>
          )}
        </div>
        <div class="flex gap-2 mt-5">
          <button
            type="submit"
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
            disabled={busy || incomplete}
          >
            {choose(busy, t("Connecting…"), t("Connect"))}
          </button>
          <button
            type="button"
            class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
            onClick={closeConnect}
          >
            {t("Cancel")}
          </button>
        </div>
      </form>
    </ModalBackdrop>
  );
}
