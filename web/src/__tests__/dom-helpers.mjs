// Small helpers several groups of tests share word for word. Each group still sets up its own jsdom
// page (cov-*-helpers.mjs): the globals, the frame clock and the timers they need differ, and a
// test leans on exactly those of its group.

// text is an element's text with its whitespace collapsed, "" for no element.
export const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

// flush lets pending promises and zero-delay timers run, n turns of the timer queue. Groups whose
// tests mock setTimeout flush on setImmediate instead, and keep their own.
export async function flush(n = 3) {
  for (let i = 0; i < n; i++) {
    await new Promise((r) => setTimeout(r, 0));
  }
}

// jsonResponse is a minimal fetch Response with a JSON body.
export function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
