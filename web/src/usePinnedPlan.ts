// The plan the conversation keeps word for word: the agent pins a plan it gives, and it is carried
// through compactions and restarts. This hook reads it, and lets the person change or drop it.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t } from "./i18n";
import { JSON_HEADERS } from "./appConst";
import type { Api } from "./prApi";
import type { SetStatus } from "./appTypes";

// planPath is the conversation's plan endpoint.
export const planPath = (sessionId: string) =>
  `/v1/sessions/${encodeURIComponent(sessionId)}/plan`;

// usePinnedPlan reads the plan when the conversation changes and again when a turn ends (the turn
// may have pinned a new one). A gateway that keeps no plan, or a failed read, shows none.
export function usePinnedPlan(
  api: Api,
  sessionId: string,
  running: boolean,
  setState: SetStatus,
) {
  const [plan, setPlan] = useState("");

  const load = useCallback(async () => {
    if (!sessionId) {
      setPlan("");
      return;
    }
    try {
      const res = await api(planPath(sessionId));
      let d = { plan: "" };
      if (res.ok) {
        d = await res.json();
      }
      setPlan(d.plan ?? "");
    } catch {
      setPlan("");
    }
  }, [api, sessionId]);

  useEffect(() => {
    if (!running) {
      void load();
    }
  }, [load, running]);

  // save replaces the plan; a blank one drops it. It answers whether the gateway took it.
  const save = useCallback(
    async (text: string) => {
      try {
        const res = await api(planPath(sessionId), {
          method: "PUT",
          headers: JSON_HEADERS,
          body: JSON.stringify({ plan: text }),
        });
        if (!res.ok) {
          setState(t("could not change the plan"), true);
          return false;
        }
        const d = await res.json();
        setPlan(d.plan ?? "");
        return true;
      } catch {
        setState(t("could not change the plan"), true);
        return false;
      }
    },
    [api, sessionId, setState],
  );

  return { plan, save, reload: load };
}
