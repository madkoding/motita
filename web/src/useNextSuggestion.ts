// The hint of the empty composer after a turn: what the person might ask next, written by the
// session's summary model. It is a courtesy: when the model cannot be asked, the field keeps its
// usual hint, and nothing here touches the run or the conversation.
import { useEffect, useRef, useState } from "preact/hooks";
import { lang } from "./i18n";
import type { Api } from "./prApi";
import type { Message } from "./appTypes";
import { JSON_HEADERS, SESSIONS_PATH } from "./appConst";
import { lastAnswer, requestOf, spokenText } from "./useSpeech";

// nextSuggestion asks the gateway what to ask next; "" means nothing came out. A refusal throws.
export async function nextSuggestion(
  api: Api,
  sessionId: string,
  body: { request: string; answer: string },
): Promise<string> {
  const res = await api(
    SESSIONS_PATH + encodeURIComponent(sessionId) + "/next-suggestion",
    {
      method: "POST",
      headers: JSON_HEADERS,
      body: JSON.stringify({ ...body, lang: lang() }),
    },
  );
  if (!res.ok) {
    throw new Error("HTTP " + res.status);
  }
  const out = (await res.json()) as { text?: string };
  return (out.text || "").trim();
}

interface Deps {
  api: Api | null;
  sessionId: string;
  running: boolean;
  messages: Message[];
}

// useNextSuggestion is the suggestion for the turn that just ended, or "" while there is none: it
// is dropped when a turn starts or the session changes.
export function useNextSuggestion({
  api,
  sessionId,
  running,
  messages,
}: Deps): string {
  const [suggestion, setSuggestion] = useState("");
  const wasRunning = useRef(running);
  const generation = useRef(0);
  const messagesRef = useRef(messages);
  messagesRef.current = messages;
  useEffect(() => {
    generation.current += 1;
    setSuggestion("");
  }, [sessionId]);
  useEffect(() => {
    const ended = wasRunning.current && !running;
    wasRunning.current = running;
    if (running) {
      generation.current += 1;
      setSuggestion("");
      return;
    }
    const answer = lastAnswer(messagesRef.current);
    if (!ended || !api || !answer) {
      return;
    }
    generation.current += 1;
    const mine = generation.current;
    nextSuggestion(api, sessionId, {
      request: requestOf(messagesRef.current, answer.id),
      answer: spokenText(answer),
    }).then(
      (text) => {
        if (mine === generation.current) {
          setSuggestion(text);
        }
      },
      () => undefined,
    );
  }, [running, api, sessionId]);
  return suggestion;
}
