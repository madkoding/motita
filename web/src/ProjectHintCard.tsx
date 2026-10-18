// The offer to give the conversation a project, above the composer: a line saying what Motita
// would do, one button to do it and one to leave it. Nothing happens until the person presses one.
import { t, tf } from "./i18n";
import { useApp } from "./appStore";
import type { ProjectHint } from "./projectHints";

// offerOf is the question and the button of an offer.
function offerOf(hint: ProjectHint): {
  ask: string;
  action: string;
  detail: string;
} {
  if (hint.kind === "start") {
    return {
      ask: tf('Move this session into a new project "{title}"?', {
        title: hint.title,
      }),
      action: t("Create project"),
      detail: t(
        "The files it made stay in its previous folder; the conversation goes on in the new project.",
      ),
    };
  }
  const { name } = hint.repo;
  if (hint.kind === "create") {
    return {
      ask: tf(
        'Create the project "{name}" from this repository and move this session into it?',
        { name },
      ),
      action: t("Create project"),
      detail: t(
        "It is cloned, indexed and set up with its defaults; this conversation goes on inside it.",
      ),
    };
  }
  if (hint.kind === "move") {
    return {
      ask: tf(
        'The project "{title}" already has {name}. Move this session into it?',
        {
          title: hint.projectTitle,
          name,
        },
      ),
      action: t("Move session"),
      detail: t(
        "The conversation stays; the agent now works in the project's own worktree.",
      ),
    };
  }
  return {
    ask: tf('Add {name} to the project "{title}"?', {
      name,
      title: hint.projectTitle,
    }),
    action: t("Add repository"),
    detail: t(
      "It is cloned once and every session of the project gets its own worktree of it.",
    ),
  };
}

export function ProjectHintCard() {
  const { projectHint } = useApp();
  const { hint, busy, accept, dismiss } = projectHint;
  if (!hint) {
    return null;
  }
  const offer = offerOf(hint);
  return (
    <output
      class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 sm:px-5 py-2 border-t border-white/5 text-xs flex-none z-10 relative"
      data-project-hint={hint.kind}
    >
      <span class="flex-1 min-w-[12rem]">
        <span class="font-semibold">{offer.ask}</span>
        <span class="block opacity-70">{offer.detail}</span>
      </span>
      <button
        type="button"
        class="px-3 min-h-[32px] rounded-lg font-semibold bg-accent text-white active:scale-95 transition-transform disabled:opacity-50"
        disabled={busy}
        onClick={() => void accept()}
      >
        {offer.action}
      </button>
      <button
        type="button"
        class="px-3 min-h-[32px] rounded-lg opacity-80 hover:opacity-100"
        disabled={busy}
        onClick={dismiss}
      >
        {t("Not now")}
      </button>
    </output>
  );
}
