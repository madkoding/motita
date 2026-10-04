import { analyzeCommits } from "@semantic-release/commit-analyzer";

const run = (msgs) =>
  analyzeCommits(
    { preset: "conventionalcommits" },
    {
      cwd: process.cwd(),
      commits: msgs.map((m) => ({ hash: "x", message: m })),
      logger: { log() {} },
    },
  );

const cases = [
  [["fix: a"], "patch"],
  [["fix: a", "feat: b"], "minor"],
  [["feat: b", "feat!: c"], "major"],
  [["fix: a", "chore: x\n\nBREAKING CHANGE: y"], "major"],
  [["chore: x", "docs: y"], null],
];

let bad = 0;
for (const [msgs, want] of cases) {
  const got = await run(msgs);
  if (got !== want) {
    console.error("FAIL", JSON.stringify(msgs), got, want);
    bad++;
  } else {
    console.log("ok", JSON.stringify(msgs), "->", got);
  }
}
process.exit(bad ? 1 : 0);
