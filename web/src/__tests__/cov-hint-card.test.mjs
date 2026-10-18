// The offer card above the composer. Run with: node --test web/src/__tests__/cov-hint-card.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  bundle,
  h,
  mount,
  click,
  buttonByText,
} from "./cov-projects-helpers.mjs";

const { AppCtx, ProjectHintCard } = await bundle({
  "ProjectHintCard.tsx": ["ProjectHintCard"],
});

const repo = { url: "https://github.com/ada/engine", name: "engine" };

async function card(hint, busy = false) {
  const log = [];
  const m = await mount(
    AppCtx,
    {
      projectHint: {
        hint,
        busy,
        accept: () => log.push("accept"),
        dismiss: () => log.push("dismiss"),
      },
    },
    h(ProjectHintCard, {}),
  );
  return { m, log };
}

test("nothing is drawn without an offer", async () => {
  const { m } = await card(null);
  assert.equal(m.root.innerHTML, "");
  await m.unmount();
});

test("each offer says what it would do and its buttons answer it", async () => {
  const cases = [
    [{ kind: "create", repo }, /Create the project "engine"/, "Create project"],
    [
      { kind: "move", repo, projectId: "a", projectTitle: "Motor" },
      /"Motor" already has engine/,
      "Move session",
    ],
    [
      { kind: "start", title: "Billing" },
      /new project "Billing"/,
      "Create project",
    ],
    [
      { kind: "add", repo, projectId: "a", projectTitle: "Motor" },
      /Add engine to the project "Motor"/,
      "Add repository",
    ],
  ];
  for (const [hint, ask, action] of cases) {
    const { m, log } = await card(hint);
    assert.equal(m.q("output").dataset.projectHint, hint.kind);
    assert.match(m.root.textContent, ask);
    await click(buttonByText(m.root, action));
    await click(buttonByText(m.root, "Not now"));
    assert.deepEqual(log, ["accept", "dismiss"]);
    await m.unmount();
  }
});

test("both buttons wait while the offer is being done", async () => {
  const { m } = await card({ kind: "create", repo }, true);
  for (const b of m.qa("button")) {
    assert.equal(b.disabled, true);
  }
  await m.unmount();
});
