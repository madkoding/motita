// Generates the Documentation pages of the site: site/docs.html plus one
// site/docs-<topic>.html per area, all styled by site/docs.css.
//
//   node scripts/gen-docs.mjs          write the pages
//   node scripts/gen-docs.mjs --check  fail when the committed pages are stale
//
// The pages are explanations for a reader who wants to know what each part of
// motita is for. They do not restate the engineering: every page ends with links
// to the sections of docs/GUIDE.md and docs/REFERENCE.md that hold the detail, so
// the numbers and the configuration keys live in exactly one place.
//
// They are generated, not hand-copied, so the header, the side menu and the
// previous/next links cannot drift apart between twelve files. They sit flat in
// site/ (docs-*.html, not docs/) because scripts/verify-site.sh resolves every
// local reference from the site root, and flat paths resolve identically on
// Vercel (cleanUrls) and under the /motita/ prefix of GitHub Pages.
import { readFileSync, writeFileSync } from "node:fs";

const REPO = "https://github.com/madkoding/motita/blob/main/docs";
const guide = (anchor) => `${REPO}/GUIDE.md#${anchor}`;
const reference = (anchor) => `${REPO}/REFERENCE.md#${anchor}`;

// Each page: slug, short menu title, one-line summary, body HTML, and the
// deeper reading as [label, url] pairs.
const pages = [
  {
    slug: "install",
    title: "Install and first run",
    blurb: "One command to install, a short setup the first time you run it.",
    body: `
<p>motita is one static binary. There is nothing to compile and nothing else to install first.</p>
<h2>Install</h2>
<pre><span class="c"># Linux and macOS</span>
curl -fsSL https://motita.kodingvibes.com/install.sh | sh

<span class="c"># Windows (PowerShell)</span>
irm https://motita.kodingvibes.com/install.ps1 | iex</pre>
<p>The installer detects your system and architecture, downloads the matching binary, and checks it against the release's <code>SHA256SUMS</code>. If the check does not match it stops and leaves your system untouched. It installs without administrator rights, then runs the binary once to prove it starts.</p>
<h2>First run</h2>
<p>Type <code>motita</code>. With no configuration, a short setup starts on its own. It has five steps and writes nothing until you have seen the result:</p>
<ol>
<li><strong>Provider.</strong> Which model service answers: OpenAI, ChatGPT (Codex), GitHub Copilot, Ollama, Anthropic, your Claude subscription, Gemini or Qwen.</li>
<li><strong>Connect.</strong> Log in with your account, or paste an API key (hidden as you paste it).</li>
<li><strong>Model.</strong> Pick one of the suggested models, or type any model ID.</li>
<li><strong>Check.</strong> What decides that a task is really done. The default is to detect it from the project: your <code>make test</code>, <code>go test</code>, <code>npm test</code>, <code>cargo test</code>. A project with an unusual build lists its own commands in <code>.motita/anchor</code>.</li>
<li><strong>Review and save.</strong> Every answer on one screen. <code>n</code> goes through them again.</li>
</ol>
<p>Run the setup again at any time with <code>/config</code> inside motita or <code>motita config</code>. It offers to keep your current key and login, and keeps the previous file as <code>motita.yaml.bak</code>.</p>
<h2>When something does not work</h2>
<p><code>motita doctor</code> checks the machine before anything runs, one line per check: the build against the system, the shell, DNS, your <code>PATH</code>, git, the configuration and the sandbox it would build. It needs no key and calls no model.</p>
<div class="note">Older and smaller machines are a target, not an afterthought: there are builds for 32-bit x86, ARM, ARM64, macOS and Windows.</div>`,
    more: [
      ["Guide: Install", guide("install")],
      ["Reference: Installation", reference("installation")],
      ["Reference: motita doctor", reference("checking-the-machine-motita-doctor")],
    ],
  },
  {
    slug: "terminal",
    title: "The terminal interface",
    blurb: "The full-screen interface you get by typing motita.",
    body: `
<p>Run <code>motita</code> with no arguments and you get a complete terminal interface, written against the Go standard library alone.</p>
<img src="shots/tui-welcome.webp" alt="The terminal interface right after the setup" width="987" height="659" loading="lazy" />
<h2>What you see</h2>
<ul>
<li><strong>Who is answering.</strong> One line names the provider and model and how hard it thinks. If there is no key, it says so in words and tells you how to add one.</li>
<li><strong>What Enter will do.</strong> The input box is titled with its mode. <strong>Task</strong> makes changes and proves them with your check. <strong>Plan</strong> only reads and explains. <kbd>Tab</kbd> switches.</li>
<li><strong>The keys that work now.</strong> The footer changes as you type a command, while a task runs, or while you scroll back.</li>
</ul>
<h2>Staying in control</h2>
<ul>
<li><kbd>Esc</kbd> stops the running task.</li>
<li>A message typed while the agent works is queued and sent when it finishes.</li>
<li><code>/</code> opens the command list. <code>?</code> shows every command and key. A plain word is always a message, so typing <code>new</code> never runs a command by accident.</li>
<li>A failed turn shows the error as it happened plus a hint for the first things newcomers meet: Ollama not running, a refused key, an unknown model, a rate limit.</li>
</ul>
<h2>Commands worth knowing</h2>
<table>
<thead><tr><th>Command</th><th>What it does</th></tr></thead>
<tbody>
<tr><td><code>/config</code></td><td>Run the setup again.</td></tr>
<tr><td><code>/models</code></td><td>List and switch the model for this conversation.</td></tr>
<tr><td><code>/sessions</code>, <code>/attach</code>, <code>/new</code>, <code>/back</code></td><td>See, join, open and leave conversations. A run keeps going while you look at another one.</td></tr>
<tr><td><code>/agents</code> (<kbd>Ctrl+G</kbd>)</td><td>The background agents and what each is doing.</td></tr>
<tr><td><code>/good</code>, <code>/bad</code></td><td>Tell motita whether the last procedure helped.</td></tr>
<tr><td><code>/git</code></td><td>Connect GitHub, GitLab or Bitbucket.</td></tr>
<tr><td><code>/artifacts</code></td><td>Files the agent saved in this conversation.</td></tr>
<tr><td><code>/language</code></td><td>Switch between English and Spanish at once.</td></tr>
<tr><td><code>/update</code></td><td>Install the newest release.</td></tr>
</tbody>
</table>
<h2>Language</h2>
<p>The terminal, the browser and the setup speak English or Spanish. By default they follow your system language; <code>ui.language</code> or <code>/language</code> chooses one for every interface.</p>`,
    more: [
      ["Guide: the terminal interface", guide("a-terminal-interface-youll-actually-want-to-use")],
      ["Reference: colour and glyphs", reference("colour-and-glyphs-in-the-terminal")],
      ["Reference: the interface language", reference("the-interface-language")],
      ["TUI design review", "TUI-DESIGN-REVIEW.md"],
    ],
  },
  {
    slug: "web",
    title: "Browser interface and gateway",
    blurb: "The same agent from a browser or a phone, served from one port.",
    body: `
<p>Every motita process is also a <strong>gateway</strong>: it listens on <code>127.0.0.1:7477</code> behind a token, and the terminal you are looking at is just one of its clients. A web page or a phone can join the same conversation, with the same skills and the same history.</p>
<img src="shots/webui-phone.webp" alt="The browser interface on a phone" class="phone" width="600" height="1298" loading="lazy" />
<h2>The browser interface</h2>
<p>It is served from the gateway's own port, so there is no second server and no cross-origin setup. Open <code>http://127.0.0.1:7477</code>, paste the token once, and you have chat, projects, settings and the skill library. It installs as an app (PWA) and is compiled into the binary.</p>
<p>The token is read from <code>~/.motita/gateway.token</code> and printed by <code>motita gateway start</code>.</p>
<h2>Running the gateway</h2>
<pre>motita gateway start    <span class="c"># start the service and wait until it answers</span>
motita gateway status   <span class="c"># is one running, where, and which build</span>
motita gateway stop
motita gateway setup    <span class="c"># where it answers: this machine, the LAN, everywhere</span></pre>
<p>If a gateway is already running, <code>motita</code> attaches to it and never shuts it down. If none is running, it brings one up for itself and stops it when you leave.</p>
<h2>The two halves apart</h2>
<pre><span class="c"># on the machine that runs the commands</span>
motita -serve -gateway 127.0.0.1:7477

<span class="c"># anywhere else: no sandbox, no agent in this process</span>
motita -connect 127.0.0.1:7477 -tui</pre>
<p>A <code>-connect</code> client is only an interface, so it runs on a laptop, a tablet or a phone through a tunnel. Reaching it from other machines safely is covered in <a href="docs-network.html">Network and remote access</a>.</p>
<h2>More than one conversation</h2>
<p>A gateway holds many sessions at once. The model belongs to the session, so changing it affects that conversation only, and survives a restart. Other programs can drive the same sessions over HTTP, server-sent events or a WebSocket protocol; the reference lists every endpoint.</p>`,
    more: [
      ["Guide: the browser interface", guide("the-browser-interface-from-the-same-port")],
      ["Guide: more than one conversation", guide("more-than-one-conversation-at-a-time")],
      ["Guide: WebSocket protocol", guide("websocket-a-bidirectional-flag-protocol")],
      ["Reference: the gateway", reference("the-gateway")],
    ],
  },
  {
    slug: "projects",
    title: "Projects, sessions and git",
    blurb: "Each session works on its own branch; pull requests are followed for you.",
    body: `
<p>Point motita at a repository and it becomes a <strong>project</strong>. Everything a session does happens in an isolated copy, so you can run several jobs at once without them touching each other or your checkout.</p>
<h2>Sessions and worktrees</h2>
<ul>
<li>Every session in a project gets <strong>its own git worktree on its own branch</strong> (<code>motita/&lt;session&gt;</code>). Dependency folders such as <code>node_modules</code> are linked rather than reinstalled.</li>
<li><strong>Every turn is a checkpoint.</strong> Go back to any turn; the conversation rewinds, and the files too when you ask.</li>
<li>Ask the agent to <strong>integrate</strong> its work and it commits and merges the branch into the project's. The session becomes read-only, and <em>continue</em> opens a fresh one from the updated base.</li>
<li><strong>Deleting asks first.</strong> You see exactly which uncommitted files would be lost.</li>
<li>Instruction files are read: <code>AGENTS.md</code> (or <code>CLAUDE.md</code>) from the git root down, and your own <code>~/.motita/AGENTS.md</code>. They guide how to work and never override your request or your security settings.</li>
</ul>
<h2>Git hosts</h2>
<p>Connect GitHub, GitLab, Bitbucket, or a self-hosted GitLab, Gitea or GitHub Enterprise once, from <strong>Settings &rarr; Git connections</strong> or <code>/git connect</code>. GitHub signs in with one click. The other hosts need either an OAuth application of yours or a token you create by hand; a token does everything the login does and is checked before it is saved.</p>
<p>After that, <code>git clone</code>, <code>pull</code> and <code>push</code> work inside a session. Logins go only to commands you approved, and are stored in <code>~/.motita/auth/</code> with owner-only permissions, never in the configuration file.</p>
<h2>Pull requests and CI</h2>
<ul>
<li>Commit messages and pull-request titles must be semantic (<code>type(scope): description</code>); anything else is refused before it is made.</li>
<li>The agent opens a pull request with <code>motita forge pr create</code> (you are asked first) and gives you its link.</li>
<li>From then on the <em>gateway</em> follows the CI, the reviews and the merge state without the model, so waiting costs no tokens. When a job fails or a reviewer comments, the agent is sent back to fix it, up to a limit you set (5 by default).</li>
<li>A project can merge by itself (<code>auto_merge</code>) and open the next session by itself (<code>auto_continue</code>).</li>
</ul>
<h2>Background agents</h2>
<p>A job with independent pieces does not have to run one piece at a time. The agent can start background agents, each on its own branch, up to <code>agent.max_parallel</code> (3 by default). Each one has a role: <code>code</code> writes, <code>review</code> and <code>research</code> read and change nothing. You see each one's purpose, state, time and token cost in the terminal (<kbd>Ctrl+G</kbd>) and in the browser's agents drawer.</p>`,
    more: [
      ["Guide: projects, sessions, and going back", guide("projects-sessions-and-going-back")],
      ["Guide: git hosts", guide("git-hosts-connect-once-and-every-session-can-push")],
      ["Guide: background agents", guide("background-agents-side-by-side")],
      ["Manual test of the PR flow", `${REPO}/PR-CI-MANUAL-TEST.md`],
    ],
  },
  {
    slug: "models",
    title: "Models and providers",
    blurb: "Bring your own model, and pick a different one for each kind of work.",
    body: `
<p>motita does not come with a model. It talks to the one you already use, and the loop around it is the same whichever answers.</p>
<h2>Providers</h2>
<table>
<thead><tr><th>Provider</th><th>How you connect</th></tr></thead>
<tbody>
<tr><td>OpenAI, and any OpenAI-compatible host</td><td>API key</td></tr>
<tr><td>ChatGPT (Codex)</td><td>Sign in with your ChatGPT plan</td></tr>
<tr><td>GitHub Copilot</td><td>GitHub device code</td></tr>
<tr><td>Anthropic</td><td>API key</td></tr>
<tr><td>Claude subscription (<code>claude-code</code>)</td><td><code>claude auth login</code> in Anthropic's own client</td></tr>
<tr><td>Google Gemini</td><td>API key or your own Google login</td></tr>
<tr><td>Qwen</td><td>DashScope API key</td></tr>
<tr><td>Ollama</td><td>Nothing on your machine; an API key for Ollama Cloud</td></tr>
</tbody>
</table>
<p>An API key always wins over a stored login. Keys you paste go to a separate owner-only file beside the configuration (<code>~/.motita/motita.env</code>), so the configuration itself can be committed and shared. A key is never sent back to the browser.</p>
<img src="shots/webui-ask.webp" alt="The browser interface answering a question" width="1600" height="1000" loading="lazy" />
<h2>From the browser</h2>
<p><strong>Settings &rarr; Providers</strong> lists every provider, where its key comes from, a field for a key and endpoint, and a button that tests the connection by listing the provider's models. Copilot, Qwen, Codex and Gemini can sign in with an account from the same page.</p>
<h2>A model for each kind of work</h2>
<p>The <strong>Models</strong> tab sets the default model and, under it, one for each kind of work. A kind left on <em>Same as default</em> follows the session.</p>
<table>
<thead><tr><th>Kind</th><th>Used for</th></tr></thead>
<tbody>
<tr><td><code>plan</code></td><td>Plan-mode turns</td></tr>
<tr><td><code>code</code></td><td>Task mode, and background agents that write</td></tr>
<tr><td><code>review</code></td><td>Background agents with the review role</td></tr>
<tr><td><code>research</code></td><td>Background agents with the research role</td></tr>
<tr><td><code>summary</code></td><td>Titles, compaction summaries, auto mode's guess</td></tr>
</tbody>
</table>
<p>A strong model for planning and a cheap one for summaries is a common split. If a kind's provider has no key, it falls back to the session's model and the log says so.</p>`,
    more: [
      ["Guide: providers and how each authenticates", guide("providers-and-how-each-one-authenticates")],
      ["Guide: your Claude subscription", guide("your-claude-subscription-through-claude-code")],
      ["Reference: LLM providers from the browser", reference("llm-providers-from-the-browser")],
      ["Reference: models per kind of work", reference("models-per-kind-of-work")],
    ],
  },
  {
    slug: "voice",
    title: "Voice",
    blurb: "Dictate to motita and have its answers read aloud.",
    body: `
<p>The browser interface can listen and speak. Voice is off until you turn it on in <strong>Settings &rarr; Voice</strong>.</p>
<h2>Speech input</h2>
<p>A microphone button beside <strong>Send</strong> dictates into the message box. The text always lands in the box to be read and edited; it is never sent on its own. A hands-free mode listens for a wake phrase (by default <em>hey motita</em>) while the tab is open.</p>
<p><strong>Who listens</strong> is your choice:</p>
<ul>
<li><strong>This browser.</strong> A Whisper model the gateway downloads once and the page runs itself, on WebGPU where available and on WebAssembly otherwise. Nothing you say leaves the device. This is the private option.</li>
<li><strong>A transcription service.</strong> The gateway forwards the recording to an OpenAI-compatible transcription endpoint (OpenAI, Groq, or a local whisper server). The audio is held in memory for that request and never written to disk.</li>
</ul>
<div class="note">With a transcription service, hands-free mode sends everything said while it listens to that endpoint. That is why hands-free is off by default.</div>
<p>The microphone only exists on a page served over https or from localhost, so over a plain LAN address the switches are greyed out with that reason. See <a href="docs-network.html">Network and remote access</a>.</p>
<h2>Reading answers aloud</h2>
<ul>
<li>The speaker button on an answer, or <kbd>Alt+L</kbd> for the last one, reads it or stops it.</li>
<li><strong>Settings &rarr; Read aloud</strong> chooses when answers are read on their own (never, only after a turn you spoke, or always), plus voice, speed and length.</li>
<li>Voices can be the device's own, or local neural voices the browser runs itself.</li>
<li><strong>Natural</strong> style talks about the turn like a colleague: a short line when it starts, one about what it is doing, and the gist of the answer. <strong>Word for word</strong> reads the text as written.</li>
<li>Code blocks and tables are named instead of read.</li>
</ul>
<p>With both on, <strong>Conversation</strong> mode listens again after each answer so you can talk back and forth. Optional subtitles show what is being said above the message box.</p>`,
    more: [
      ["Reference: voice input", reference("voice-input")],
      ["Reference: reading answers aloud", reference("reading-answers-aloud")],
    ],
  },
  {
    slug: "skills",
    title: "Skills: what it learns",
    blurb: "A shelf of procedures it reads, writes and keeps tidy, no retraining.",
    body: `
<p>motita keeps a <strong>library of procedures</strong>: documents describing how a kind of work is done, with the steps, the commands that work and the pitfalls someone already paid for. The model looks one up when it needs it and writes a new one when it learns something. They are plain files, so you can read, fix and version them.</p>
<h2>How it improves</h2>
<ul>
<li>A fresh install starts with built-in procedures (running commands, files, git, HTTP APIs, web search, toolchains, verifying a change, diagrams and reports).</li>
<li><code>/good</code> and <code>/bad</code> land on the procedures that were actually used, and the library is searched by what has worked before.</li>
<li>No fine-tuning, no extra API, no extra bill. A ledger next to a shelf.</li>
</ul>
<h2>See and control the shelf</h2>
<p>The browser has a <strong>Skill library</strong> window: every document, what it is for, how often it was used, the document itself rendered, and a switch to turn one off without losing it.</p>
<p>A project can keep its own procedures in <code>&lt;project&gt;/.motita/skills/</code>; they are searched first and shadow the shared library.</p>
<h2>The curator</h2>
<p>Left alone, a shelf rots, so a curator runs weekly, only when nobody has talked to motita for two hours. It uses no model for the work that matters and touches only documents the agent wrote:</p>
<ul>
<li>Unused for 14 days: marked <strong>stale</strong>, still searchable.</li>
<li>Stale for 30 more days: <strong>archived</strong>, never deleted, one command brings it back.</li>
<li><strong>Pinned</strong> documents are exempt from every automatic step. Nothing automatic ever deletes.</li>
</ul>
<pre>motita curator status
motita curator run --dry-run
motita curator pin build-firmware</pre>`,
    more: [
      ["Guide: it learns from you", guide("it-learns-from-you-not-from-a-retraining-pipeline")],
      ["Guide: the shelf keeps itself tidy", guide("you-can-see-the-shelf-and-the-shelf-keeps-itself-tidy")],
      ["Reference: the skill library", reference("the-skill-library")],
    ],
  },
  {
    slug: "security",
    title: "Safety and the sandbox",
    blurb: "What it can never do, what it asks about, and how much disk it reaches.",
    body: `
<p>An agent that runs commands needs limits that do not depend on the model behaving. motita has two layers, and only one of them is yours.</p>
<h2>The floor</h2>
<p>Some commands are refused every time: disk formatting and partition tools, <code>shred</code>, <code>dd</code> to an output, power-state commands, and <code>rm</code>, <code>mv</code>, <code>cp</code> or <code>ln</code> aimed at <code>/</code>, your home, a system path, or the folder that contains your workspace (including after <code>sudo</code>, <code>xargs</code> or <code>find -exec</code>). <strong>No key, variable or flag relaxes the floor.</strong></p>
<h2>Allow, ask, deny</h2>
<table>
<thead><tr><th>Verdict</th><th>When</th></tr></thead>
<tbody>
<tr><td>Allow</td><td>The command changes nothing, or only the workspace: <code>make</code>, <code>go build</code>, <code>npm test</code>, your own <code>./scripts/*</code>. It runs silently, because a policy that asks about those gets switched off.</td></tr>
<tr><td>Ask</td><td>It reaches the network or the system, writes outside the workspace, or cannot be classified. You are asked before it runs.</td></tr>
<tr><td>Deny</td><td>Strict mode and the line cannot be placed.</td></tr>
</tbody>
</table>
<h2>The sandbox</h2>
<p>Commands run confined, with no Docker. Memory, CPU time, processes, open files and output size are limited, optionally with no network inside the command. On Linux with Landlock, a command cannot write outside the directories it works in and cannot read motita's logins, configuration or your git credential store.</p>
<h2>Disk access</h2>
<p><strong>Settings &rarr; Security</strong> chooses how much of the disk the agent reaches:</p>
<ul>
<li><strong>Workspace only</strong> (default): file edits outside the session's folder are refused, and on Linux a command cannot read or list your files elsewhere (home, <code>/home</code>, <code>/mnt</code>, ...).</li>
<li><strong>Whole disk</strong>: both reach anywhere your account can. The page asks first and spells out the risk. motita's own logins stay hidden either way.</li>
</ul>
<p>Where nothing confines a command (Windows, macOS, Linux without Landlock), the agent asks before the project's own code runs, unless you turn that off.</p>
<p>Each session also gets its own HOME, and cannot read the other projects and sessions of the same gateway.</p>`,
    more: [
      ["Guide: guardrails that can't be switched off", guide("guardrails-that-cant-be-switched-off")],
      ["Reference: guardrails and the two layers", reference("guardrails-and-the-two-layers-of-them")],
      ["Reference: disk access", reference("disk-access")],
      ["Reference: what runs silently", reference("what-runs-silently-and-why-that-is-a-design-decision")],
    ],
  },
  {
    slug: "network",
    title: "Network and remote access",
    blurb: "Keep it on this machine, share it on your LAN, or reach it from a phone.",
    body: `
<p>By default the gateway answers only on <code>127.0.0.1:7477</code>, so only the machine it runs on can reach it. Everything below is a deliberate step away from that.</p>
<h2>Who can reach it</h2>
<p>The first <code>gateway start</code> asks once, and <strong>Settings &rarr; Network</strong> or <code>motita gateway setup</code> changes it later:</p>
<table>
<thead><tr><th>Answer</th><th>Result</th></tr></thead>
<tbody>
<tr><td>This machine only (default)</td><td>Loopback.</td></tr>
<tr><td>The local network</td><td>Listens on all interfaces, with <code>allow: ["lan", "!any"]</code>.</td></tr>
<tr><td>Everywhere</td><td>Listens on all interfaces with no origin rules.</td></tr>
</tbody>
</table>
<p><code>gateway.allow</code> is an ordered list of rules (<code>lan</code>, an address, a network, each optionally prefixed with <code>!</code>). The first match wins, and loopback is always allowed so you cannot lock yourself out. The token stays the real key; the rules say where it may come from.</p>
<div class="note">The gateway speaks plain HTTP. Off loopback the token travels unencrypted, and the gateway warns you when it opens.</div>
<h2>Reaching it safely</h2>
<ul>
<li><strong>SSH tunnel.</strong> <code>ssh -N -L 7477:127.0.0.1:7477 the-host</code> makes it <code>http://localhost:7477</code> on your computer, with nothing exposed.</li>
<li><strong>Tailscale.</strong> If Tailscale is running, motita can publish the gateway with <code>tailscale serve</code>: https on your tailnet name, reachable only from your own devices. Funnel (the public internet) is never used.</li>
<li><strong>A TLS reverse proxy</strong> such as Caddy in front of a loopback gateway, or the gateway's own second https listener (<code>gateway.tls</code>).</li>
</ul>
<h2>Why https matters</h2>
<p>Browsers allow the microphone, desktop notifications and phone push only on https or localhost. A phone on the LAN over plain HTTP gets none of those, which is the usual reason to set one of the options above.</p>
<h2>Notifications</h2>
<p>When a pull request's CI ends, the page shows a toast. <strong>Phone notifications</strong> use standard Web Push from the gateway itself, so they arrive with the page closed. On an iPhone or iPad the page must be added to the Home Screen first (iOS 16.4 or later). <strong>Settings &rarr; Notifications</strong> has a test button for the whole path.</p>`,
    more: [
      ["Reference: the gateway", reference("the-gateway")],
      ["Guide: the browser interface and notifications", guide("the-browser-interface-from-the-same-port")],
    ],
  },
  {
    slug: "automation",
    title: "Updates and scheduled tasks",
    blurb: "Stay on the newest verified release, and run tasks on a cadence.",
    body: `
<h2>Updating</h2>
<p><code>/update</code> installs the newest release. The welcome screen and the browser's sidebar tell you when one exists. The download must match the release's <code>SHA256SUMS</code> or nothing is installed. A gateway started with <code>-serve</code> restarts itself on the new binary; an interface tells you to restart it.</p>
<p>The gateway reports its own build through <code>motita gateway status</code>, so after installing over an old binary you can tell whether the process still running is the previous one.</p>
<p>Releases are cut automatically from semantic commits: <code>feat</code> bumps the minor version, <code>fix</code> the patch, and a breaking change the major. The newest version is always shown on the <a href="index.html">home page</a>.</p>
<h2>Scheduled tasks</h2>
<p>A <strong>scheduled task</strong> is a task or a plan the gateway submits on its own, on a cadence, with nobody at the keyboard. Each one is a small JSON file in <code>~/.motita/schedules/</code> that you can read and edit by hand.</p>
<ul>
<li>The cadence is a duration (<code>30m</code>, <code>6h</code>, <code>24h</code>), not cron syntax. <code>every 6h</code> means six hours after the last run.</li>
<li>The shortest allowed cadence is one minute (<code>schedule.min_every</code>).</li>
<li>A task can be a <code>plan</code> (read-only) or a <code>task</code>, and can be pinned to a session.</li>
<li>Create, change, run-now and delete them through <code>/v1/schedules</code>, or from the interface.</li>
</ul>
<pre>curl -s -X POST -H "Authorization: Bearer $TOKEN" \\
  -H 'Content-Type: application/json' \\
  -d '{"title":"nightly audit","task":"check the logs and report","kind":"plan","every":"24h"}' \\
  http://127.0.0.1:7477/v1/schedules</pre>`,
    more: [
      ["Reference: scheduled tasks", reference("scheduled-tasks")],
      ["Guide: automatic releases", guide("automatic-releases")],
    ],
  },
];

const esc = (s) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

const file = (p) => (p ? `docs-${p.slug}.html` : "docs.html");

const brand = `<svg viewBox="0 0 32 32" width="20" height="20" aria-hidden="true"><g stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><path d="M8 24 Q4 18 6 12 L4 6 L12 11 Q16 8 20 11 L28 6 L26 12 Q28 18 24 24 Z"/><circle cx="13" cy="16" r="1.5" fill="currentColor" stroke="none"/><circle cx="21" cy="16" r="1.5" fill="currentColor" stroke="none"/><path d="M16 20 L18 22 L20 20"/><path d="M24 24 Q28 22 30 18"/></g></svg>`;

function layout({ title, description, current, main, prev, next }) {
  const side = [
    `<a href="docs.html"${current === null ? ' aria-current="page"' : ""}>Overview</a>`,
    ...pages.map(
      (p) => `<a href="${file(p)}"${current === p ? ' aria-current="page"' : ""}>${esc(p.title)}</a>`,
    ),
  ].join("\n          ");
  const pn = [prev, next].some(Boolean)
    ? `<nav class="pn" aria-label="Previous and next page">
        ${prev ? `<a class="prev" href="${file(prev === "overview" ? null : prev)}"><span>Previous</span>${esc(prev === "overview" ? "Overview" : prev.title)}</a>` : "<span></span>"}
        ${next ? `<a class="next" href="${file(next)}"><span>Next</span>${esc(next.title)}</a>` : "<span></span>"}
      </nav>`
    : "";
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>${esc(title)} · Motita docs</title>
    <meta name="description" content="${esc(description)}" />
    <meta name="color-scheme" content="dark" />
    <meta name="theme-color" content="#0a0a0f" />
    <link rel="preload" href="fonts/Sansation-Regular.woff2" as="font" type="font/woff2" crossorigin />
    <link rel="stylesheet" href="docs.css" />
  </head>
  <body>
    <a class="skip" href="#main">Skip to content</a>
    <header>
      <div class="wrap bar">
        <a class="brand" href="index.html" aria-label="Motita, home">${brand}Motita</a>
        <nav aria-label="Site">
          <a href="index.html">Home</a>
          <a href="docs.html" aria-current="true">Docs</a>
          <a href="REFERENCE.md">Reference</a>
          <a href="https://github.com/madkoding/motita">GitHub</a>
        </nav>
      </div>
    </header>
    <div class="wrap layout">
      <nav class="side" aria-label="Documentation">
        <p class="side-title">Documentation</p>
        <div class="side-links">
          ${side}
        </div>
      </nav>
      <main id="main">
${main}
      ${pn}
      </main>
    </div>
    <footer>
      <div class="wrap frow">
        <span class="mono">✦ motita</span>
        <span>GPL-3.0-or-later licensed.</span>
        <span class="sep"></span>
        <a href="index.html">Home</a>
        <a href="docs.html">Docs</a>
        <a href="REFERENCE.md">Reference</a>
        <a href="https://github.com/madkoding/motita">GitHub</a>
      </div>
    </footer>
  </body>
</html>
`;
}

const out = {};

out["docs.html"] = layout({
  title: "Documentation",
  description:
    "What each part of Motita is for: install, terminal, browser, projects, models, voice, skills, safety, network and updates.",
  current: null,
  next: pages[0],
  main: `      <h1>Documentation</h1>
      <p class="lead">Motita is one program with several parts. Pick the one you want to understand; each page explains what it is for, how to use it, and where the full detail lives.</p>
      <div class="cards">
        ${pages
          .map(
            (p) =>
              `<a class="card" href="${file(p)}"><h2>${esc(p.title)}</h2><p>${esc(p.blurb)}</p></a>`,
          )
          .join("\n        ")}
      </div>
      <h2>Looking for the engineering detail?</h2>
      <p>These pages explain. For every configuration key, endpoint and the architecture in full, read the <a href="REFERENCE.md">reference</a> or the <a href="${REPO}/GUIDE.md">complete guide</a>. For how the terminal interface was designed, see the <a href="TUI-DESIGN-REVIEW.md">TUI design review</a>.</p>`,
});

pages.forEach((p, i) => {
  out[file(p)] = layout({
    title: p.title,
    description: p.blurb,
    current: p,
    prev: i === 0 ? "overview" : pages[i - 1],
    next: pages[i + 1],
    main: `      <h1>${esc(p.title)}</h1>
      <p class="lead">${esc(p.blurb)}</p>
${p.body.trim()}
      <h2>Go deeper</h2>
      <ul class="more">
        ${p.more
          .map(([label, url]) => `<li><a href="${esc(url)}">${esc(label)}</a></li>`)
          .join("\n        ")}
      </ul>`,
  });
});

const check = process.argv.includes("--check");
let stale = 0;
for (const [name, html] of Object.entries(out)) {
  const path = new URL(`../site/${name}`, import.meta.url);
  if (check) {
    let have = "";
    try {
      have = readFileSync(path, "utf8");
    } catch {
      // missing counts as stale
    }
    if (have !== html) {
      process.stderr.write(`site/${name} is stale: run node scripts/gen-docs.mjs\n`);
      stale++;
    }
  } else {
    writeFileSync(path, html);
  }
}
if (stale) process.exit(1);
console.info(check ? "docs pages are up to date" : `wrote ${Object.keys(out).length} docs pages`);
