// Build step for the Vercel mirror of the site (motita.kodingvibes.com).
//
// site/ is static and needs no bundling; the one write is the same version stamp
// pages.yml applies before uploading to GitHub Pages. Vercel clones shallow and
// without tags, so the version comes from the releases API instead of git, and
// only from a release that already lists SHA256SUMS (the binaries are uploaded).
//
// It never fails the build: offline or rate-limited, the marker keeps its
// fallback and the page's own fetch of releases/latest replaces it in the browser.
// The marker itself is still required, because a page without it would never show
// a version at all.
//
// It also adds Vercel Web Analytics to every page. Only here, so GitHub Pages and a
// local checkout never load it: /_vercel/insights/script.js exists only on Vercel.
// The loader is cookieless and skips visitors with Do Not Track or Global Privacy
// Control on. The webui that runs on the user's machine never gets analytics.
import { readFileSync, writeFileSync } from "node:fs";

const page = new URL("../site/index.html", import.meta.url);
const marker = /(<span data-motita-version>)[^<]*(<\/span>)/g;

// The loader lives in its own file so it stays plain HTML, reviewed as such.
const analytics = readFileSync(
  new URL("vercel-analytics.html", import.meta.url),
  "utf8",
);

for (const name of ["index.html", "architecture.html"]) {
  const file = new URL(`../site/${name}`, import.meta.url);
  const html = readFileSync(file, "utf8");
  if (html.includes("data-vercel-analytics")) {
    continue;
  }
  if (html.split("</head>").length !== 2) {
    console.error("site/" + name + " needs exactly one closing head tag");
    process.exit(1);
  }
  writeFileSync(file, html.replace("</head>", analytics + "  </head>"));
  console.log(`added Vercel Web Analytics to site/${name}`);
}

const src = readFileSync(page, "utf8");
const found = src.match(marker)?.length ?? 0;
if (found !== 1) {
  console.error(
    `site/index.html has ${found} version markers; expected exactly one`,
  );
  process.exit(1);
}

async function latestRelease() {
  const res = await fetch(
    "https://api.github.com/repos/madkoding/motita/releases/latest",
    {
      headers: { Accept: "application/vnd.github+json" },
      signal: AbortSignal.timeout(10_000),
    },
  );
  if (!res.ok) {
    throw new Error(`releases API answered ${res.status}`);
  }
  const rel = await res.json();
  if (!/^v\d+\.\d+\.\d+$/.test(rel.tag_name ?? "")) {
    throw new Error(`unexpected tag ${JSON.stringify(rel.tag_name)}`);
  }
  if (!(rel.assets ?? []).some((a) => a.name === "SHA256SUMS")) {
    throw new Error(`${rel.tag_name} has no SHA256SUMS yet`);
  }
  return rel.tag_name;
}

try {
  const version = await latestRelease();
  writeFileSync(
    page,
    src.replace(marker, (_, open, close) => open + version + close),
  );
  console.log(`stamped site/index.html with ${version}`);
} catch (err) {
  console.warn(
    `version not stamped (${err.message}); the page fetches it at runtime`,
  );
}
