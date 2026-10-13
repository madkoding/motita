// showNotification: through the service worker where there is one (Android's Chrome only allows
// that), and the Notification constructor otherwise.
// Run with: node --test web/src/__tests__/notify.test.mjs
import test from "node:test";
import assert from "node:assert/strict";

const { showNotification } = await import("../notify.ts");

function withNavigator(value, fn) {
  const before = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { value, configurable: true });
  return fn().finally(() => {
    if (before) {
      Object.defineProperty(globalThis, "navigator", before);
    }
  });
}

test("the service worker shows it when one is registered", async () => {
  const shown = [];
  globalThis.Notification = function () {
    throw new TypeError("Illegal constructor");
  };
  await withNavigator(
    {
      serviceWorker: {
        getRegistration: async () => ({
          showNotification: async (t, o) => shown.push([t, o.body]),
        }),
      },
    },
    async () => {
      assert.equal(await showNotification("CI passed", { body: "x" }), true);
    },
  );
  assert.deepEqual(shown, [["CI passed", "x"]]);
});

test("without a worker the constructor is used, and a refusal is reported", async () => {
  const made = [];
  globalThis.Notification = function (t) {
    made.push(t);
  };
  await withNavigator({}, async () => {
    assert.equal(await showNotification("CI failed", {}), true);
  });
  assert.deepEqual(made, ["CI failed"]);
  globalThis.Notification = function () {
    throw new TypeError("Illegal constructor");
  };
  await withNavigator(
    { serviceWorker: { getRegistration: async () => undefined } },
    async () => {
      assert.equal(await showNotification("CI failed", {}), false);
    },
  );
});
