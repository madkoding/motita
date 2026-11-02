// The project's open pull requests, as rows of its box beside its sessions: each one is a link to
// the pull request on its host, with its CI state on the row (a ring while it runs, a dot once it
// has passed or failed), read from the shared overview (pullRequestsLive) and followed while it runs.
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { IconPullRequest } from "./icons";
import {
  ciBadge,
  failingFirst,
  type PRItem,
  type Tone,
} from "./pullRequestsApi";
import { useProjectPullRequests } from "./pullRequestsLive";

// ciWord is the CI state in words.
export function ciWord(state: string): string {
  if (state === "success") {
    return t("passing");
  }
  if (state === "failure") {
    return t("failing");
  }
  if (state === "pending") {
    return t("running");
  }
  return t("no checks");
}

const TONE_TEXT: Record<Tone, string> = {
  good: "text-[#40f0a0]",
  bad: "text-danger",
  wait: "text-warning",
  idle: "text-[#9a9aaa]",
};

// prTooltip is the row's title: the pull request, its branches and every check with its state.
function prTooltip(item: PRItem): string {
  const lines = [
    tf("Open pull request #{n} on its host", { n: String(item.pr.number) }),
  ];
  if (item.pr.head) {
    lines.push(item.pr.head + " → " + (item.pr.base ?? ""));
  }
  for (const c of item.ci?.checks ?? []) {
    lines.push(c.name + ": " + ciWord(c.state));
  }
  return lines.join("\n");
}

function PullRequestRow({ item }: { item: PRItem }) {
  const ci = ciBadge(item);
  return (
    <a
      class="session-row pr-thread-row flex items-center gap-2 px-3 py-2 rounded-lg mb-0.5 border border-transparent hover:bg-white/5"
      href={item.pr.url}
      target="_blank"
      rel="noopener noreferrer"
      title={prTooltip(item)}
      data-testid="project-pr"
    >
      <span class={`pr-ci-mark is-${ci.tone}`} aria-hidden="true" />
      <div class="flex-1 min-w-0">
        <div class="truncate text-sm leading-tight text-[#e8e8ea]">
          <IconPullRequest
            size={11}
            class="inline-block mr-1 text-[#8a8a9a]"
            style="vertical-align: -1px"
          />
          #{item.pr.number} {item.pr.title}
        </div>
        <div class="session-when mt-0.5 text-[10px] tabular-nums text-[#9a9aaa]">
          <span class={TONE_TEXT[ci.tone]} data-testid="project-pr-ci">
            {t("CI")} {ciWord(ci.label)}
            {choose(!!ci.detail, " " + ci.detail, "")}
          </span>
        </div>
      </div>
    </a>
  );
}

// ProjectPullRequests are the project's open pull requests; nothing when it has none.
export function ProjectPullRequests({ projectId }: { projectId: string }) {
  const items = failingFirst(useProjectPullRequests(projectId));
  return (
    <>
      {items.map((i) => (
        <PullRequestRow key={i.repo + "#" + i.pr.number} item={i} />
      ))}
    </>
  );
}
