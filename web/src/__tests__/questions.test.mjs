// The questions the agent asks before doing anything: the card that answers them (QuestionsCard.tsx)
// run for real in jsdom, the request the answers are sent in (questions.ts), and how a conversation
// opened again draws them (transcript.ts).
// Run with: node --test web/src/__tests__/questions.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text } from "./cov-render-helpers.mjs";

const dom = setupDom();
const W = dom.window;

const q = await load("questions.ts");
const { QuestionsCard } = await load("QuestionsCard.tsx");
const { transcriptMessages } = await load("transcript.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const ITEMS = [
  { text: "which login?", assumption: "email", options: ["email", "GitHub"] },
  { text: "where do sessions live?", options: ["cookies", "Redis"] },
  { text: "anything else?", assumption: "nothing" },
];

async function mount(props) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() =>
    render(
      h(QuestionsCard, { items: ITEMS, origin: "add login", ...props }),
      root,
    ),
  );
  return {
    root,
    async done() {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

const click = (el) =>
  act(() => el.dispatchEvent(new W.MouseEvent("click", { bubbles: true })));
const type = (el, value) =>
  act(() => {
    el.value = value;
    el.dispatchEvent(new W.Event("input", { bubbles: true }));
  });
const submit = (root) =>
  act(() =>
    root
      .querySelector("form")
      .dispatchEvent(
        new W.Event("submit", { bubbles: true, cancelable: true }),
      ),
  );
const questions = (root) => [...root.querySelectorAll(".qcard-list > li")];
const options = (li) => [...li.querySelectorAll(".qcard-opts button")];
const optionText = (b) => b.querySelector(".qcard-opt-text").textContent;
const key = (b) => b.querySelector(".qcard-key").textContent;

test("answerRequest sends each question with its answer, or its assumption when blank", () => {
  assert.equal(
    q.answerRequest(" add login ", ITEMS, ["GitHub", "", " 2FA "]),
    "add login\n\nAnswers to what you asked:\n- which login? -> GitHub\n- anything else? -> 2FA",
  );
  assert.equal(
    q.answerRequest("add login", ITEMS, []),
    "add login\n\nAnswers to what you asked:\n- which login? -> email\n- anything else? -> nothing",
  );
  assert.equal(
    q.answerRequest("add login", [{ text: "x?" }], [""]),
    "add login",
    "nothing to send",
  );
  assert.equal(
    q.answerRequest("", [{ text: "x?" }], ["y"]),
    "Answers to what you asked:\n- x? -> y",
  );
});

test("turnTask is the task the gateway keeps: trimmed, without a leading /plan or /task", () => {
  assert.equal(q.turnTask("  add login "), "add login");
  assert.equal(q.turnTask("/task add login"), "add login");
  assert.equal(q.turnTask("/T  add login "), "add login");
  assert.equal(q.turnTask("/p why?"), "why?");
  assert.equal(q.turnTask("/plan"), "");
  assert.equal(
    q.turnTask("/goal ship it"),
    "/goal ship it",
    "/goal is kept as typed",
  );
  assert.equal(q.turnTask("/tasks add"), "/tasks add");
  assert.equal(q.turnTask("plain"), "plain");
});

test("answersLabel is the answers in order, the assumption standing in for a blank one", () => {
  assert.equal(q.answersLabel(ITEMS, ["GitHub"]), "GitHub · nothing");
  assert.equal(q.answersLabel([{ text: "x?" }], [" "]), "");
});

test("isAnswering and hasQuestions", () => {
  assert.equal(q.isAnswering({ answers: [], request: "r" }, "r"), true);
  assert.equal(q.isAnswering({ answers: [], request: "r" }, "s"), false);
  assert.equal(q.isAnswering({ answers: [], request: "" }, ""), false);
  assert.equal(q.isAnswering(undefined, "r"), false);
  assert.equal(q.hasQuestions([{ text: "x" }]), true);
  assert.equal(q.hasQuestions([]), false);
  assert.equal(q.hasQuestions(null), false);
});

test("every question is on the card with its options, a field of its own and its assumption", async () => {
  const { root, done } = await mount({ onAnswer() {} });
  assert.equal(
    text(root.querySelector(".qcard-head strong")),
    "Motita needs 3 answers to continue",
  );
  assert.equal(text(root.querySelector(".qcard-count")), "0 of 3 answered");
  const qs = questions(root);
  assert.equal(qs.length, 3);
  assert.deepEqual(options(qs[0]).map(optionText), ["email", "GitHub"]);
  assert.deepEqual(
    options(qs[0]).map(key),
    ["1", "2"],
    "each option is a numbered row",
  );
  assert.equal(text(qs[1].querySelector(".qcard-step")), "Question 2 of 3");
  assert.equal(
    options(qs[2]).length,
    0,
    "a question without options has only the field",
  );
  assert.equal(
    qs[0].querySelector(".qcard-own").placeholder,
    "Or write your own answer",
  );
  assert.equal(
    qs[2].querySelector(".qcard-own").placeholder,
    "Write your answer",
  );
  assert.equal(
    text(qs[0].querySelector(".qcard-assume")),
    "If you leave it blank: email",
  );
  assert.equal(
    qs[1].querySelector(".qcard-assume"),
    null,
    "no assumption, no line",
  );
  assert.ok(root.querySelector(".qcard-hint"));
  await done();
});

test("two cards in one conversation name each control by its own question", async () => {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const card = (items) =>
    h(QuestionsCard, { items, origin: "add login", onAnswer() {} });
  await act(() =>
    render(
      h("div", {}, [
        card(ITEMS),
        card([{ text: "which database?", options: ["Postgres", "SQLite"] }]),
      ]),
      root,
    ),
  );
  const named = (el) =>
    document.getElementById(el.getAttribute("aria-labelledby")).textContent;
  const q = questions(root).at(-1);
  assert.equal(
    named(q.querySelector('[role="radiogroup"]')),
    "which database?",
  );
  assert.equal(named(q.querySelector(".qcard-own")), "which database?");
  const ids = questions(root).map((li) => li.querySelector(".qcard-q").id);
  assert.equal(new Set(ids).size, 4, "no id is used twice");
  await act(() => render(null, root));
  root.remove();
});

test("an option is picked and unpicked, a typed answer replaces it, and Send answers sends them all", async () => {
  const sent = [];
  const { root, done } = await mount({
    onAnswer: (a, shown) => sent.push([a, shown]),
  });
  let qs = questions(root);
  await click(options(qs[0])[1]);
  qs = questions(root);
  assert.equal(options(qs[0])[1].getAttribute("aria-checked"), "true");
  assert.equal(
    key(options(qs[0])[1]),
    "✓",
    "the picked option's number becomes a check",
  );
  assert.equal(options(qs[0])[1].className, "qcard-opt picked");
  assert.equal(qs[0].className, "qcard-item answered");
  assert.equal(
    text(qs[0].querySelector(".qcard-step")),
    "Question 1 of 3✓ Answered",
  );
  assert.equal(
    qs[0].querySelector(".qcard-assume"),
    null,
    "an answered question drops its assumption",
  );
  assert.equal(text(root.querySelector(".qcard-count")), "1 of 3 answered");
  await click(options(qs[0])[1]);
  assert.equal(
    options(questions(root)[0])[1].getAttribute("aria-checked"),
    "false",
    "picked again clears it",
  );
  await click(options(questions(root)[0])[1]);
  await type(questions(root)[1].querySelector(".qcard-own"), "Postgres");
  qs = questions(root);
  assert.ok(
    options(qs[1]).every((b) => b.getAttribute("aria-checked") === "false"),
  );
  assert.equal(qs[1].querySelector(".qcard-own").value, "Postgres");
  await submit(root);
  assert.deepEqual(sent, [
    [
      {
        answers: ["GitHub", "Postgres", ""],
        request:
          "add login\n\nAnswers to what you asked:\n- which login? -> GitHub\n" +
          "- where do sessions live? -> Postgres\n- anything else? -> nothing",
      },
      "GitHub · Postgres · nothing",
    ],
  ]);
  await done();
});

test("answered questions stay as they were answered, and cannot be sent again", async () => {
  const sent = [];
  const answers = { answers: ["email", "Postgres", ""], request: "r" };
  const { root, done } = await mount({
    answers,
    onAnswer: (a) => sent.push(a),
  });
  const qs = questions(root);
  assert.equal(options(qs[0])[0].getAttribute("aria-checked"), "true");
  assert.ok(options(qs[0]).every((b) => b.disabled));
  assert.equal(
    root.querySelector(".qcard-own"),
    null,
    "no field once answered",
  );
  assert.equal(text(qs[1].querySelector(".qcard-yours")), "Postgres");
  assert.equal(text(root.querySelector(".qcard-done")), "✓ Answers sent");
  assert.equal(root.querySelector(".qcard-send"), null);
  assert.equal(root.querySelector(".qcard-hint"), null);
  assert.match(root.querySelector("form").className, /sent/);
  await submit(root);
  assert.deepEqual(sent, []);
  await done();
});

test("questions the conversation moved on from are no longer offered; one with no handler cannot be sent", async () => {
  const stale = await mount({ expired: true, onAnswer() {} });
  assert.match(stale.root.querySelector("form").className, /expired/);
  assert.ok(stale.root.querySelector(".qcard-note"));
  assert.equal(stale.root.querySelector(".qcard-send").disabled, true);
  assert.equal(stale.root.querySelector(".qcard-hint"), null);
  await stale.done();

  const busy = await mount({});
  assert.equal(busy.root.querySelector(".qcard-send").disabled, true);
  await submit(busy.root);
  await busy.done();

  const one = await mount({ items: [{ text: "which?" }], onAnswer() {} });
  assert.equal(
    text(one.root.querySelector(".qcard-head strong")),
    "Motita needs an answer to continue",
  );
  assert.equal(
    one.root.querySelector(".qcard-count"),
    null,
    "one question needs no tally",
  );
  assert.equal(one.root.querySelector(".qcard-step"), null, "nor a position");
  await one.done();
});

test("a conversation opened again draws the questions, their answers, and the answering turn by its answers", () => {
  let id = 0;
  const nextId = () => ++id;
  const questionsOut = [{ text: "which?", options: ["a", "b"] }];
  const qa = { answers: ["b"], request: "add\n\nanswers" };
  const msgs = transcriptMessages(
    [
      { User: "add", Agent: "which?" },
      { User: "add\n\nanswers", Agent: "done" },
      { User: "again", Agent: "which?" },
    ],
    [
      {
        turn: 0,
        task: "add",
        files: false,
        steps: [],
        outcome: { result: "which?", questions: questionsOut },
        question_answers: qa,
      },
      { turn: 1, files: false, steps: [], outcome: { result: "done" } },
      {
        turn: 2,
        files: false,
        steps: [],
        outcome: { result: "which?", questions: questionsOut },
      },
    ],
    nextId,
  );
  const asked = msgs[1];
  assert.deepEqual(
    [asked.questions, asked.origin, asked.questionAnswers],
    [questionsOut, "add", qa],
  );
  assert.equal(msgs[2].shown, "b", "the answering turn reads as its answers");
  assert.equal(msgs[3].questions, undefined);
  assert.equal(msgs[3].origin, undefined);
  assert.equal(
    msgs[4].shown,
    undefined,
    "a turn that does not carry the answers is drawn as typed",
  );
  assert.equal(
    msgs[5].origin,
    "",
    "a checkpoint without its task has no origin to send back",
  );
});
