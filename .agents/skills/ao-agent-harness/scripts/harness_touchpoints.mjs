#!/usr/bin/env node
// Reports which AO source touchpoints already register a harness, grouped by
// product surface. With --like REF it also prints where an existing harness is
// wired in each file, so a contributor (or agent) knows exactly where to add
// the new entry. Read-only: it never edits the checkout.

import { readdir, readFile, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export const SURFACES = Object.freeze(["tui", "install", "auth", "models", "branding", "docs", "chat", "reviewer"]);

const escapeRe = value => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** Default Go package name: the harness ID without separators (mimo-code -> mimocode). */
export function defaultPackage(id) {
  return id.toLowerCase().replace(/[^a-z0-9]/g, "");
}

function linesMatching(text, regex) {
  const flags = regex.flags.includes("g") ? regex.flags : `${regex.flags}g`;
  const global = new RegExp(regex.source, flags);
  const lines = [];
  for (const match of text.matchAll(global)) lines.push(text.slice(0, match.index).split("\n").length);
  return [...new Set(lines)];
}

async function readText(repo, file) {
  try { return await readFile(join(repo, file), "utf8"); } catch { return null; }
}

async function exists(repo, path) {
  try { await stat(join(repo, path)); return true; } catch { return false; }
}

// Constant declared as `<Name> <Type> = "<id>"`, and that constant listed in `<listName>`.
function constantInList(text, type, listName, id) {
  const declaration = text.match(new RegExp(`\\b(\\w+)\\s+${type}\\s*=\\s*"${escapeRe(id)}"`));
  if (!declaration) return { ok: false, detail: `no ${type} constant for "${id}"` };
  const list = text.match(new RegExp(`${listName}\\s*=\\s*\\[\\]${type}\\s*\\{([\\s\\S]*?)\\n\\}`));
  if (!list) return { ok: false, detail: `${listName} block not found` };
  const listed = new RegExp(`\\b${declaration[1]}\\b`).test(list[1]);
  return { ok: listed, detail: listed ? `${declaration[1]} in ${listName}` : `${declaration[1]} declared but missing from ${listName}` };
}

// Every struct-tag enum of the given Go type that lists at least `minimum`
// harnesses must include the ID. Narrow enums (handoff targets) are skipped.
function enumTags(text, goType, id, minimum = 10) {
  const tags = [...text.matchAll(new RegExp(`${goType}\\s+\`[^\`]*enum:"([^"]+)"`, "g"))]
    .map(match => match[1].split(","))
    .filter(values => values.length >= minimum);
  if (!tags.length) return { ok: false, detail: `no ${goType} enum tags found` };
  const missing = tags.filter(values => !values.includes(id)).length;
  return { ok: missing === 0, detail: `${tags.length - missing}/${tags.length} ${goType} enums include "${id}"` };
}

/**
 * Touchpoint table. `required` means the surface is not wired without it;
 * other rows are conventional follow-ups that reviewers expect.
 * `like` builds the regex that locates the reference harness in the same file.
 */
export const TOUCHPOINTS = Object.freeze([
  { id: "adapter-package", surface: "tui", required: true, file: "backend/internal/adapters/agent/{pkg}/",
    check: async (ctx) => ({ ok: await exists(ctx.repo, `backend/internal/adapters/agent/${ctx.pkg}`) }) },
  { id: "domain-harness", surface: "tui", required: true, file: "backend/internal/domain/harness.go",
    check: (ctx, text) => constantInList(text, "AgentHarness", "AllHarnesses", ctx.id),
    like: ref => new RegExp(`AgentHarness\\s*=\\s*"${escapeRe(ref.id)}"`) },
  { id: "agent-registry", surface: "tui", required: true, file: "backend/internal/adapters/agent/registry/registry.go",
    check: (ctx, text) => ({ ok: text.includes(`adapters/agent/${ctx.pkg}"`) && new RegExp(`\\b${escapeRe(ctx.pkg)}\\.New\\(`).test(text) }),
    like: ref => new RegExp(`adapters/agent/${escapeRe(ref.pkg)}"|\\b${escapeRe(ref.pkg)}\\.New\\(`) },
  { id: "sqlite-constraint", surface: "tui", required: true, file: "backend/internal/storage/sqlite/db.go",
    check: (ctx, text) => ({ ok: text.includes(`'${ctx.id}'`), detail: "sessions.harness CHECK repair constants" }),
    like: ref => new RegExp(`'${escapeRe(ref.id)}'`) },
  { id: "sqlite-migration", surface: "tui", required: true, file: "backend/internal/storage/sqlite/migrations/",
    check: async (ctx) => {
      const dir = join(ctx.repo, "backend/internal/storage/sqlite/migrations");
      let names = [];
      try { names = (await readdir(dir)).filter(name => name.endsWith(".sql")).sort(); } catch { return { ok: false, detail: "migrations directory not found" }; }
      const latest = names.at(-1) || "none";
      for (const name of names) {
        if ((await readFile(join(dir, name), "utf8")).includes(`'${ctx.id}'`)) return { ok: true, detail: name };
      }
      return { ok: false, detail: `no migration widens the CHECK; latest is ${latest}` };
    } },
  { id: "api-dto-enums", surface: "tui", required: true, file: "backend/internal/httpd/controllers/dto.go",
    check: (ctx, text) => enumTags(text, "domain.AgentHarness", ctx.id),
    like: ref => new RegExp(`domain\\.AgentHarness\\s+\`[^\`]*enum:"[^"]*\\b${escapeRe(ref.id)}\\b`) },
  { id: "openapi-spec", surface: "tui", required: true, file: "backend/internal/httpd/apispec/openapi.yaml",
    check: (ctx, text) => ({ ok: new RegExp(`^\\s*- ${escapeRe(ctx.id)}\\s*$`, "m").test(text), detail: "regenerate with npm run api" }),
    like: ref => new RegExp(`^\\s*- ${escapeRe(ref.id)}\\s*$`, "m") },
  { id: "generated-schema", surface: "tui", required: true, file: "frontend/src/api/schema.ts",
    check: (ctx, text) => ({ ok: text.includes(`"${ctx.id}"`), detail: "regenerate with npm run api" }) },
  { id: "product-ui-label", surface: "tui", required: true, file: "packages/product-ui/src/agents.ts",
    check: (ctx, text) => ({ ok: new RegExp(`"${escapeRe(ctx.id)}"\\s*:\\s*"`).test(text) && new RegExp(`^\\s*"${escapeRe(ctx.id)}",\\s*$`, "m").test(text) }),
    like: ref => new RegExp(`"${escapeRe(ref.id)}"`) },
  { id: "activity-dispatch", surface: "tui", required: false, file: "backend/internal/adapters/agent/activitydispatch/dispatch.go",
    check: (ctx, text) => ({ ok: new RegExp(`"${escapeRe(ctx.id)}"\\s*:`).test(text) }),
    like: ref => new RegExp(`"${escapeRe(ref.id)}"\\s*:|adapters/agent/${escapeRe(ref.pkg)}"`) },
  { id: "cli-spawn-help", surface: "tui", required: false, file: "backend/internal/cli/spawn.go",
    check: (ctx, text) => ({ ok: new RegExp(`[ ,]${escapeRe(ctx.id)}[ ,(]`).test(text) }),
    like: ref => new RegExp(`[ ,]${escapeRe(ref.id)}[ ,(]`) },
  { id: "install-target", surface: "install", required: true, file: "backend/internal/service/systeminstall/systeminstall.go",
    check: (ctx, text) => constantInList(text, "Target", "agentTargets", ctx.id),
    like: ref => new RegExp(`Target\\s*=\\s*"${escapeRe(ref.id)}"`) },
  { id: "install-plan", surface: "install", required: true, file: "backend/internal/service/systeminstall/agentplans.go",
    check: async (ctx, text) => {
      const source = await readText(ctx.repo, "backend/internal/service/systeminstall/systeminstall.go") || "";
      const name = source.match(new RegExp(`\\b(Target\\w*)\\s+Target\\s*=\\s*"${escapeRe(ctx.id)}"`))?.[1];
      return name ? { ok: new RegExp(`case[^:]*\\b${name}\\b`).test(text), detail: `case ${name}` } : { ok: false, detail: "no install Target constant" };
    } },
  { id: "auth-plan", surface: "auth", required: true, file: "backend/internal/service/agentauth/plans.go",
    check: (ctx, text) => ({ ok: new RegExp(`plan\\(\\s*"${escapeRe(ctx.id)}"`).test(text) }),
    like: ref => new RegExp(`plan\\(\\s*"${escapeRe(ref.id)}"`) },
  { id: "model-catalog", surface: "models", required: true, file: "backend/internal/adapters/agent/modelcatalog/catalog.go",
    check: (ctx, text) => ({ ok: text.includes(`"${ctx.id}"`) }),
    like: ref => new RegExp(`"${escapeRe(ref.id)}"`) },
  { id: "avatar-asset", surface: "branding", required: true, file: "frontend/src/renderer/assets/agents/{id}.svg|png",
    check: async (ctx) => ({ ok: await exists(ctx.repo, `frontend/src/renderer/assets/agents/${ctx.id}.svg`) || await exists(ctx.repo, `frontend/src/renderer/assets/agents/${ctx.id}.png`) }) },
  { id: "avatar-license", surface: "branding", required: true, file: "frontend/src/renderer/assets/agents/LICENSE-{id}.txt",
    check: async (ctx) => ({ ok: await exists(ctx.repo, `frontend/src/renderer/assets/agents/LICENSE-${ctx.id}.txt`) }) },
  { id: "avatar-map", surface: "branding", required: true, file: "frontend/src/renderer/components/AgentAvatar.tsx",
    check: (ctx, text) => ({ ok: new RegExp(`"${escapeRe(ctx.id)}"\\s*:`).test(text) }),
    like: ref => new RegExp(`"${escapeRe(ref.id)}"\\s*:|agents/${escapeRe(ref.id)}\\.`) },
  { id: "harness-doc", surface: "docs", required: true, file: "docs/harnesses/{id}.md",
    check: async (ctx) => ({ ok: await exists(ctx.repo, `docs/harnesses/${ctx.id}.md`) }) },
  { id: "docs-index", surface: "docs", required: false, file: "docs/README.md",
    check: (ctx, text) => ({ ok: text.includes(`harnesses/${ctx.id}.md`) }),
    like: ref => new RegExp(`harnesses/${escapeRe(ref.id)}\\.md`) },
  { id: "chat-registry", surface: "chat", required: true, file: "backend/internal/adapters/chatdriver/registry/registry.go",
    check: (ctx, text) => ({ ok: new RegExp(`\\w+\\.New\\(\\s*${escapeRe(ctx.pkg)}\\.New\\(`).test(text) }),
    like: ref => new RegExp(`\\w+\\.New\\(\\s*${escapeRe(ref.pkg)}\\.New\\(|adapters/agent/${escapeRe(ref.pkg)}"`) },
  { id: "reviewer-harness", surface: "reviewer", required: true, file: "backend/internal/domain/reviewerharness.go",
    check: (ctx, text) => constantInList(text, "ReviewerHarness", "AllReviewerHarnesses", ctx.id),
    like: ref => new RegExp(`ReviewerHarness\\s*=\\s*"${escapeRe(ref.id)}"`) },
  { id: "reviewer-registry", surface: "reviewer", required: true, file: "backend/internal/adapters/reviewer/registry.go",
    check: (ctx, text) => ({ ok: text.includes(`adapters/reviewer/${ctx.reviewerPkg}"`) }),
    like: ref => new RegExp(`adapters/reviewer/${escapeRe(ref.reviewerPkg)}"|\\b${escapeRe(ref.reviewerPkg)}\\.New\\(`) },
  { id: "reviewer-dto-enums", surface: "reviewer", required: true, file: "backend/internal/httpd/controllers/dto.go",
    check: (ctx, text) => enumTags(text, "domain.ReviewerHarness", ctx.id, 3),
    like: ref => new RegExp(`domain\\.ReviewerHarness\\s+\`[^\`]*enum:"[^"]*\\b${escapeRe(ref.id)}\\b`) },
]);

function context(repo, harness) {
  const pkg = harness.pkg || defaultPackage(harness.id);
  return { repo, id: harness.id, pkg, reviewerPkg: harness.reviewerPkg || pkg };
}

export async function scanTouchpoints({ repo, harness, like = null, surfaces = SURFACES }) {
  const root = resolve(repo);
  const ctx = context(root, harness);
  const ref = like ? context(root, like) : null;
  const rows = [];
  for (const touchpoint of TOUCHPOINTS.filter(item => surfaces.includes(item.surface))) {
    const file = touchpoint.file.replaceAll("{id}", ctx.id).replaceAll("{pkg}", ctx.pkg);
    const isDirectory = file.endsWith("/") || file.includes("|");
    const text = isDirectory ? "" : await readText(root, file);
    let outcome;
    if (!isDirectory && text === null) outcome = { ok: false, detail: "file not found in this checkout", missingFile: true };
    else outcome = await touchpoint.check(ctx, text);
    const row = { id: touchpoint.id, surface: touchpoint.surface, required: touchpoint.required, file, wired: outcome.ok === true };
    if (outcome.detail) row.detail = outcome.detail;
    if (outcome.missingFile) row.missingFile = true;
    if (ref && touchpoint.like && text) {
      const lines = linesMatching(text, touchpoint.like(ref));
      if (lines.length) row.likeLines = lines.slice(0, 8);
    }
    rows.push(row);
  }
  const summary = {};
  for (const surface of surfaces) {
    const required = rows.filter(row => row.surface === surface && row.required);
    const all = rows.filter(row => row.surface === surface);
    const wired = required.filter(row => row.wired).length;
    summary[surface] = {
      requiredWired: wired, requiredTotal: required.length,
      state: wired === required.length ? "wired" : wired === 0 && !all.some(row => row.wired) ? "absent" : "partial",
    };
  }
  return { schemaVersion: 1, repo: root, harness: { id: ctx.id, pkg: ctx.pkg, reviewerPkg: ctx.reviewerPkg }, like: ref ? { id: ref.id, pkg: ref.pkg } : null, summary, touchpoints: rows };
}

export function formatTouchpoints(scan) {
  const lines = [`Touchpoints for ${scan.harness.id} (Go package ${scan.harness.pkg})${scan.like ? `, reference ${scan.like.id}` : ""}`];
  for (const [surface, value] of Object.entries(scan.summary)) {
    lines.push("", `## ${surface}: ${value.state} (${value.requiredWired}/${value.requiredTotal} required)`);
    for (const row of scan.touchpoints.filter(item => item.surface === surface)) {
      const mark = row.wired ? "ok  " : row.required ? "TODO" : "opt ";
      const hint = !row.wired && row.likeLines ? ` -> add beside ${scan.like.id} at ${row.file}:${row.likeLines.join(",")}` : "";
      lines.push(`  [${mark}] ${row.id.padEnd(20)} ${row.file}${row.detail ? ` (${row.detail})` : ""}${hint}`);
    }
  }
  return `${lines.join("\n")}\n`;
}

function parseArgs(argv) {
  const options = { repo: process.cwd(), harness: "", pkg: "", reviewerPkg: "", like: "", likePkg: "", surfaces: [...SURFACES], require: [], json: false };
  const values = { "--repo": "repo", "--harness": "harness", "--pkg": "pkg", "--reviewer-pkg": "reviewerPkg", "--like": "like", "--like-pkg": "likePkg" };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--json") options.json = true;
    else if (argument === "-h" || argument === "--help") options.help = true;
    else if (argument === "--surfaces" || argument === "--require") {
      const list = (argv[++index] || "").split(",").map(item => item.trim()).filter(Boolean);
      const unknown = list.filter(item => !SURFACES.includes(item));
      if (unknown.length) throw new Error(`unknown surface: ${unknown.join(", ")}`);
      options[argument.slice(2)] = list;
    } else if (values[argument]) {
      const value = argv[++index];
      if (value === undefined) throw new Error(`missing value for ${argument}`);
      options[values[argument]] = value;
    } else throw new Error(`unknown option: ${argument}`);
  }
  if (!options.help && !/^[a-z0-9][a-z0-9-]*$/.test(options.harness)) throw new Error("--harness ID is required (lowercase letters, digits and dashes)");
  return options;
}

const HELP = `Usage: node scripts/harness_touchpoints.mjs --harness ID [options]

Report which AO source touchpoints register a harness (read-only).

  --repo PATH           AO checkout (default: current directory)
  --harness ID          harness ID as stored in sessions.harness, e.g. mimo-code
  --pkg NAME            Go adapter package (default: ID without dashes)
  --reviewer-pkg NAME   reviewer adapter package (default: --pkg)
  --like REF            existing harness to locate as an insertion example
  --like-pkg NAME       Go package of REF when it differs from REF without dashes
  --surfaces LIST       comma list of ${SURFACES.join(",")}
  --require LIST        exit 1 unless these surfaces are fully wired
  --json                print JSON (for harness_plan.mjs ingest-touchpoints)
`;

async function main(argv = process.argv.slice(2)) {
  let options;
  try { options = parseArgs(argv); } catch (error) { console.error(`error: ${error.message}`); return 2; }
  if (options.help) { console.log(HELP); return 0; }
  if (!await exists(resolve(options.repo), "backend/internal/domain/harness.go")) {
    console.error(`error: ${resolve(options.repo)} does not look like an AO checkout (backend/internal/domain/harness.go missing)`);
    return 2;
  }
  const scan = await scanTouchpoints({
    repo: options.repo,
    harness: { id: options.harness, pkg: options.pkg, reviewerPkg: options.reviewerPkg },
    like: options.like ? { id: options.like, pkg: options.likePkg } : null,
    surfaces: options.surfaces,
  });
  process.stdout.write(options.json ? `${JSON.stringify(scan, null, 2)}\n` : formatTouchpoints(scan));
  const incomplete = options.require.filter(surface => scan.summary[surface]?.state !== "wired");
  if (incomplete.length) {
    console.error(`not fully wired: ${incomplete.join(", ")}`);
    return 1;
  }
  return 0;
}

const invokedAsScript = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedAsScript) process.exitCode = await main();
