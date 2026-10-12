// The session's provider, model and reasoning level, and the Model Switcher that changes them.
import { useCallback, useState } from "preact/hooks";
import { reasoningOf } from "./appFormat";
import type { Api } from "./prApi";
import type { ConfigView, ProviderInfo, SetStatus } from "./appTypes";
import { JSON_HEADERS, SESSIONS_PATH } from "./appConst";

export function useModelSwitcher(
  api: Api,
  sessionId: string,
  setState: SetStatus,
) {
  const [config, setConfig] = useState<ConfigView | null>(null);
  const [showModelSwitcher, setShowModelSwitcher] = useState(false);
  // draftProvider/draftModel are the Model Switcher's working copy. The modal
  // edits THESE, never the saved configuration: a select the user is merely
  // exploring must not change the running session, so nothing is sent to the
  // gateway until Done is pressed. The draft is re-seeded from the saved
  // configuration every time the modal opens, and closing without Done drops it.
  const [draftProvider, setDraftProvider] = useState("");
  const [draftModel, setDraftModel] = useState("");
  const [draftReasoning, setDraftReasoning] = useState("medium");
  const [savingConfig, setSavingConfig] = useState(false);
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [modelList, setModelList] = useState<string[]>([]);
  const [fetchingModels, setFetchingModels] = useState(false);

  // fetchProviders loads the available providers for the current session.
  const fetchProviders = useCallback(async () => {
    if (!sessionId) {
      return;
    }
    try {
      const res = await api(SESSIONS_PATH + sessionId + "/providers");
      const data = await res.json();
      setProviders(data.providers || []);
    } catch {
      // non-fatal: the modal opens with an empty list
    }
  }, [sessionId]);

  // fetchModelList fetches the live model list from the provider API.
  //
  // It lists the models of the provider the GATEWAY is configured with, which is
  // the saved one and not the draft: the endpoint takes no provider argument. That
  // is why the modal only shows this list while the draft still points at the saved
  // provider, and falls back to the catalogue otherwise.
  const fetchModelList = useCallback(async () => {
    if (!sessionId) {
      return;
    }
    setFetchingModels(true);
    setModelList([]);
    try {
      const res = await api(SESSIONS_PATH + sessionId + "/model-list");
      if (res.ok) {
        const data = await res.json();
        setModelList(data.models || []);
      }
    } catch {
      // non-fatal: the select shows the static catalog instead
    }
    setFetchingModels(false);
  }, [sessionId]);

  // openModelSwitcher shows the modal with a draft seeded from the SAVED
  // configuration. Seeding on open, rather than on every change, is what makes
  // Done the only commit point: an edit that was cancelled and a modal that was
  // dismissed both leave the session's provider and model untouched.
  const openModelSwitcher = useCallback(() => {
    setDraftProvider(config?.provider || "");
    setDraftModel(config?.model || "");
    setDraftReasoning(reasoningOf(config));
    fetchProviders();
    fetchModelList();
    setShowModelSwitcher(true);
  }, [config, fetchProviders, fetchModelList]);

  // closeModelSwitcher dismisses the modal and throws the draft away. The saved
  // configuration is what the next open seeds from, so nothing has to be undone.
  //
  // Nothing is sent here even when the draft differs: Done is the only commit
  // point, and a dismissal is not one. A user who picks a model and then changes
  // their mind must be able to walk away from the choice.
  const closeModelSwitcher = useCallback(() => {
    setShowModelSwitcher(false);
  }, []);

  // discardModelSwitcher is the Close (✕) button: it drops the draft and reopens
  // the modal on what the session is actually running, so the screen cannot be
  // left showing a selection that was never applied.
  const discardModelSwitcher = useCallback(() => {
    setDraftProvider(config?.provider || "");
    setDraftModel(config?.model || "");
    setDraftReasoning(reasoningOf(config));
    setShowModelSwitcher(false);
  }, [config]);

  // applyProviderModel is what Done does, and it is the ONE call that reaches the
  // gateway. Both fields travel together: the server treats an empty model as
  // "leave unchanged", so a provider switch sent alone would keep a model that the
  // new provider may not serve.
  const applyProviderModel = useCallback(async () => {
    if (!sessionId) {
      return;
    }
    setSavingConfig(true);
    try {
      const res = await api(SESSIONS_PATH + sessionId + "/config", {
        method: "PATCH",
        headers: JSON_HEADERS,
        body: JSON.stringify({
          provider: draftProvider,
          model: draftModel,
          reasoning: draftReasoning,
        }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        setState(err.error || "could not update the configuration", true);
        // The modal stays open on a failure: the choice is still on screen and
        // can be retried, and a closed modal would read as if it had worked.
        return;
      }
      const updated: ConfigView = await res.json();
      setConfig(updated);
      setShowModelSwitcher(false);
    } catch {
      setState("could not update the configuration", true);
    } finally {
      // In a finally, not at the end of the try: the failure path returns early,
      // and a flag left set would leave Done disabled and the spinner running
      // with no way back.
      setSavingConfig(false);
    }
  }, [sessionId, draftProvider, draftModel, draftReasoning]);

  return {
    config,
    setConfig,
    showModelSwitcher,
    draftProvider,
    setDraftProvider,
    draftModel,
    setDraftModel,
    draftReasoning,
    setDraftReasoning,
    savingConfig,
    providers,
    modelList,
    fetchingModels,
    openModelSwitcher,
    closeModelSwitcher,
    discardModelSwitcher,
    applyProviderModel,
  };
}

export type ModelSwitcher = ReturnType<typeof useModelSwitcher>;
