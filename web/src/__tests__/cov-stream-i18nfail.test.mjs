// When the Spanish dictionary cannot be fetched (an old cached shell, a dropped connection), the
// page stays in English rather than showing a half-translated screen.
import test from "node:test";
import assert from "node:assert/strict";
import { installDom, unreachable, source } from "./cov-stream-helpers.mjs";

installDom();
unreachable(["i18n.es.ts"]);
const lone = await source("i18n.ts");

test("a Spanish dictionary that cannot be fetched leaves the page in English", async () => {
  await lone.setLang("es");
  assert.equal(lone.lang(), "en");
  assert.equal(document.documentElement.lang, "en");
  assert.equal(lone.t("New session"), "New session");
});
