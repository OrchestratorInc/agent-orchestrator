#!/usr/bin/env node
// Step 0 of a harness integration: from a link or name, answer
//   1. Is it already integrated in AO?          (touchpoint scan of the checkout)
//   2. Is there already a PR for it?            (gh search, all states)
//   3. What does upstream offer?                (GitHub repo, releases, README
//      hints, and the local CLI's --help when installed)
// and print the decision: continue an existing PR, finish a partial
// integration, or start a new one. Read-only.
//
//   node scripts/harness_intake.mjs --link https://github.com/org/agent-cli [--id agent-cli] [--binary agent] [--repo .]

import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { scanTouchpoints } from "./harness_touchpoints.mjs";

function run(command, args, { cwd, timeoutMs = 30_000 } = {}) {
  return new Promise((done) => {
    execFile(command, args, { cwd, timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024, encoding: "utf8" }, (error, stdout, stderr) => {
      done({ ok: !error, stdout: stdout ?? "", stderr: stderr ?? "", missing: error?.code === "ENOENT" });
    });
  });
}

export function parseLink(link) {
  if (!link) return {};
  const github = link.match(/github\.com\/([\w.-]+)\/([\w.-]+?)(?:\.git)?(?:[/#?]|$)/i);
  if (github) return { kind: "github", owner: github[1], repo: github[2], name: github[2] };
  const npm = link.match(/npmjs\.com\/package\/((?:@[\w.-]+\/)?[\w.-]+)/i);
  if (npm) return { kind: "npm", pkg: npm[1], name: npm[1].split("/").pop() };
  const pypi = link.match(/pypi\.org\/project\/([\w.-]+)/i);
  if (pypi) return { kind: "pypi", pkg: pypi[1], name: pypi[1] };
  return { kind: "web", name: new URL(link).hostname.split(".").slice(-2, -1)[0] };
}

export function harnessId(name) {
  return String(name).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").replace(/-(cli|agent|code)$/, (match, word) => (word === "code" ? match : ""));
}

// What a README or --help says the CLI supports. Hints only: each must be
// confirmed against the pinned binary before it is wired into AO.
const SIGNALS = [
  ["tui", /interactive|tui|terminal ui|repl/i],
  ["headless", /--print\b|-p\b|exec\b|non-interactive|headless|--prompt/i],
  ["acp", /\bacp\b|agent client protocol|agentclientprotocol/i],
  ["resume", /--resume|--continue|resume (a |the )?session|session id/i],
  ["system_prompt", /system[- ]prompt|--instructions|append-system|rules file|AGENTS\.md/i],
  ["models", /\bmodels?\b.*(list|--model)|--model\b/i],
  ["login", /\blogin\b|auth(enticate)?\b|api[_ -]?key/i],
  ["mcp", /\bmcp\b/i],
  ["hooks", /\bhooks?\b/i],
];
const INSTALLERS = [
  ["npm", /npm (i|install)( -g)? |npx /i],
  ["pip/uv", /pip(x)? install|uv tool install|uvx /i],
  ["brew", /brew install/i],
  ["curl-script", /curl [^\n]*\|\s*(ba)?sh/i],
  ["powershell", /irm [^\n]*\|\s*iex|powershell/i],
  ["winget/scoop", /winget install|scoop install/i],
  ["cargo", /cargo install/i],
  ["go", /go install/i],
];

export function signalsFrom(text) {
  return {
    capabilities: SIGNALS.filter(([, pattern]) => pattern.test(text)).map(([id]) => id),
    installers: INSTALLERS.filter(([, pattern]) => pattern.test(text)).map(([id]) => id),
  };
}

function osCoverage(assetNames) {
  const names = assetNames.join(" ").toLowerCase();
  return ["darwin|mac|apple", "linux", "windows|win64|\\.exe|\\.msi"].map((pattern, index) => [["macOS", "Linux", "Windows"][index], new RegExp(pattern).test(names)])
    .filter(([, present]) => present).map(([os]) => os);
}

async function upstreamFacts(link) {
  if (link.kind !== "github") return { note: "not a GitHub link; read the install and CLI docs by hand" };
  const repo = await run("gh", ["api", `repos/${link.owner}/${link.repo}`, "--jq", "{description, stars: .stargazers_count, license: .license.spdx_id, homepage, pushed: .pushed_at, archived}"]);
  const release = await run("gh", ["api", `repos/${link.owner}/${link.repo}/releases/latest`, "--jq", "{tag: .tag_name, published: .published_at, assets: [.assets[].name]}"]);
  const readme = await run("gh", ["api", `repos/${link.owner}/${link.repo}/readme`, "-H", "Accept: application/vnd.github.raw"]);
  const facts = {
    repository: repo.ok ? JSON.parse(repo.stdout) : { error: repo.missing ? "gh not installed" : repo.stderr.trim() },
    latestRelease: release.ok ? JSON.parse(release.stdout) : null,
  };
  if (facts.latestRelease) facts.releaseOs = osCoverage(facts.latestRelease.assets ?? []);
  if (readme.ok) facts.readme = signalsFrom(readme.stdout);
  return facts;
}

async function localCli(binary) {
  if (!binary) return null;
  const help = await run(binary, ["--help"], { timeoutMs: 15_000 });
  if (help.missing) return { binary, installed: false };
  const version = await run(binary, ["--version"], { timeoutMs: 15_000 });
  const text = `${help.stdout}\n${help.stderr}`;
  return { binary, installed: true, version: (version.stdout || version.stderr).trim().split("\n")[0], ...signalsFrom(text) };
}

async function existingPrs({ ghRepo, terms }) {
  const seen = new Map();
  for (const term of terms) {
    const listed = await run("gh", ["pr", "list", "-R", ghRepo, "--state", "all", "--search", `${term} in:title`, "--limit", "20", "--json", "number,title,state,url,isDraft,updatedAt"]);
    if (!listed.ok) return { error: listed.missing ? "gh not installed" : listed.stderr.trim() };
    for (const pr of JSON.parse(listed.stdout || "[]")) seen.set(pr.number, pr);
  }
  return [...seen.values()].sort((a, b) => b.number - a.number);
}

// A PR "covers" the harness only when its title reads like an integration;
// an already-integrated harness has many unrelated PRs that mention it.
const INTEGRATION_TITLE = /harness|integrat|adapter|\badd\b|support|native (acp|tui)|plugin/i;

export function decide({ scan, prs }) {
  const list = Array.isArray(prs) ? prs.filter((pr) => INTEGRATION_TITLE.test(pr.title)) : [];
  const open = list.filter((pr) => pr.state === "OPEN");
  const merged = list.filter((pr) => pr.state === "MERGED");
  const wired = Object.entries(scan.summary).filter(([, value]) => value.state === "wired").map(([surface]) => surface);
  const partial = Object.entries(scan.summary).filter(([, value]) => value.state === "partial").map(([surface]) => surface);
  const refs = (items) => items.map((pr) => `#${pr.number}`).join(", ");
  if (wired.includes("tui") || wired.includes("chat")) {
    const missing = Object.entries(scan.summary).filter(([, value]) => value.state !== "wired").map(([surface]) => surface);
    return { action: "complete_existing", detail: `Already integrated in this checkout (wired: ${wired.join(", ")}). ${missing.length ? `Touchpoint scan flags as incomplete (verify; built-in harnesses use older wiring): ${missing.join(", ")}. ` : ""}Run the audit (step 5) and fix what it finds.${open.length ? ` Related open PRs: ${refs(open)}.` : ""}` };
  }
  const stale = merged.length ? ` Note: ${refs(merged)} merged, but this checkout has none of it: update the checkout to current main first.` : "";
  if (open.length) return { action: "continue_existing_pr", detail: `Open PR ${refs(open)} already integrates this harness: review and continue it instead of starting a duplicate.${stale}` };
  if (stale) return { action: "update_checkout", detail: stale.trim() };
  return { action: "new_integration", detail: `Not integrated${partial.length ? ` (partial wiring: ${partial.join(", ")})` : ""} and no integration PR found. Start at step 1 (compatibility).` };
}

export function formatIntake(result) {
  const lines = [`# Intake: ${result.id}`, "", `Decision: **${result.decision.action}**. ${result.decision.detail}`, "", "## In this AO checkout"];
  for (const [surface, value] of Object.entries(result.scan.summary)) lines.push(`- ${surface}: ${value.state} (${value.requiredWired}/${value.requiredTotal} required touchpoints)`);
  lines.push("", "## Existing PRs");
  if (Array.isArray(result.prs) && result.prs.length) for (const pr of result.prs) lines.push(`- #${pr.number} ${pr.state}${pr.isDraft ? " (draft)" : ""}: ${pr.title} ${pr.url}`);
  else lines.push(Array.isArray(result.prs) ? "- none found" : `- search failed: ${result.prs.error}`);
  lines.push("", "## Upstream (hints; verify against the pinned binary)");
  const up = result.upstream;
  if (up.repository?.error) lines.push(`- GitHub lookup failed: ${up.repository.error.split("\n")[0]}`);
  else if (up.repository) lines.push(`- ${up.repository.description ?? ""} · license ${up.repository.license ?? "unknown"} · ${up.repository.stars ?? "?"} stars${up.repository.archived ? " · ARCHIVED" : ""}`);
  if (up.latestRelease) lines.push(`- latest release ${up.latestRelease.tag} (${up.latestRelease.published}); prebuilt for: ${up.releaseOs?.join(", ") || "no OS-named assets"}`);
  if (up.readme) lines.push(`- README mentions: ${up.readme.capabilities.join(", ") || "nothing recognised"}; installers: ${up.readme.installers.join(", ") || "none recognised"}`);
  if (up.note) lines.push(`- ${up.note}`);
  if (result.cli) {
    lines.push(result.cli.installed
      ? `- local \`${result.cli.binary}\` ${result.cli.version}: --help mentions ${result.cli.capabilities.join(", ") || "nothing recognised"}`
      : `- \`${result.cli.binary}\` is not installed locally`);
  }
  lines.push("", "Next: fill the compatibility table in references/integration-guide.md (step 1) from these hints and the CLI's real behaviour.");
  return `${lines.join("\n")}\n`;
}

export async function intake({ link = "", id = "", binary = "", repo = process.cwd(), ghRepo = "OrchestratorInc/agent-orchestrator" }) {
  const parsed = link ? parseLink(link) : {};
  const harness = id || harnessId(parsed.name || binary);
  if (!harness) throw new Error("give --link or --id");
  const scan = await scanTouchpoints({ repo, harness: { id: harness } });
  const terms = [...new Set([harness, parsed.name, binary].filter(Boolean))];
  const [prs, upstream, cli] = await Promise.all([existingPrs({ ghRepo, terms }), upstreamFacts(parsed), localCli(binary)]);
  return { schemaVersion: 1, id: harness, link: link || null, scan, prs, upstream, cli, decision: decide({ scan, prs }) };
}

async function main(argv = process.argv.slice(2)) {
  const options = { link: "", id: "", binary: "", repo: process.cwd(), ghRepo: "OrchestratorInc/agent-orchestrator", json: false };
  const keys = { "--link": "link", "--id": "id", "--binary": "binary", "--repo": "repo", "--gh-repo": "ghRepo" };
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === "--json") options.json = true;
    else if (argv[index] === "-h" || argv[index] === "--help") {
      console.log("Usage: node scripts/harness_intake.mjs --link URL [--id HARNESS_ID] [--binary CLI] [--repo AO_CHECKOUT] [--gh-repo OWNER/REPO] [--json]");
      return 0;
    } else if (keys[argv[index]] && argv[index + 1] !== undefined) options[keys[argv[index]]] = argv[++index];
    else { console.error(`error: unknown or incomplete option ${argv[index]}`); return 2; }
  }
  const result = await intake(options);
  process.stdout.write(options.json ? `${JSON.stringify(result, null, 2)}\n` : formatIntake(result));
  return 0;
}

const invokedAsScript = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedAsScript) process.exitCode = await main();
