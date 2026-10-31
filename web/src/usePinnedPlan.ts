// The plan the conversation keeps word for word, carried through compactions and restarts. A plan
// the agent gives because the person asked for one is "proposed"; once the person approves it (a
// go-ahead, or saving it here) it is "active", and it is let go when its work is done. This hook
// reads it with its state, and lets the person change, approve or drop it.
import { useCallback, useEffect, useState } from "preact/hooks";
import { t } from "./i18n";
import { JSON_HEADERS } from "./appConst";
import type { Api } from "./prApi";
import type { SetStatus } from "./appTypes";

// planPath is the conversation's plan endpoint.
export const planPath = (sessionId: string) =>
  `/v1/sessions/${encodeURIComponent(sessionId)}/plan`;

// PlanState is how far the pinned plan got: proposed by the agent, or approved and in force.
export type PlanState = "" | "active" | "proposed";

// planState reads the state a gateway answers; one that predates states only pins active plans.
export function planState(d: { plan?: string; state?: string }): PlanState {
  if (!d.plan) {
    return "";
  }
  if (d.state === "proposed") {
    return "proposed";
  }
  return "active";
}

// usePinnedPlan reads the plan when the conversation changes and again when a turn ends (the turn
// may have pinned a new one). A gateway that keeps no plan, or a failed read, shows none.
export function usePinnedPlan(
  api: Api,
  sessionId: string,
  running: boolean,
  setState: SetStatus,
) {
  const [plan, setPlan] = useState("");
  const [state, setPinState] = useState<PlanState>("");

  const load = useCallback(async () => {
    if (!sessionId) {
      setPlan("");
      setPinState("");
      return;
    }
    try {
      const res = await api(planPath(sessionId));
      let d = { plan: "" };
      if (res.ok) {
        d = await res.json();
      }
      setPlan(d.plan ?? "");
      setPinState(planState(d));
    } catch {
      setPlan("");
      setPinState("");
    }
  }, [api, sessionId]);

  useEffect(() => {
    if (!running) {
      void load();
    }
  }, [load, running]);

  // save replaces the plan, which approves it; a blank one drops it. It answers whether the gateway took it.
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
        setPinState(planState(d));
        return true;
      } catch {
        setState(t("could not change the plan"), true);
        return false;
      }
    },
    [api, sessionId, setState],
  );

  return { plan, state, save, reload: load };
}
