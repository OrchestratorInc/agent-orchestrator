#!/usr/bin/env node
// PR-level checks for a harness integration branch, run before every push and
// after every merge from main. They catch the failures that kept recurring
// across harness PRs (#5507–#6553): audit evidence committed to the repo,
// migration numbers that collide with main, unformatted Go, shared code
// changed without cross-harness tests, drift behind main, and red CI that
// nobody read. Read-only except for `gh`/`git` queries.
//
//   node scripts/harness_pr_check.mjs --repo . --base upstream/main [--pr 6541] [--json]

import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

function run(command, args, { cwd, timeoutMs = 60_000 } = {}) {
  return new Promise((done) => {
    execFile(command, args, { cwd, timeout: timeoutMs, maxBuffer: 32 * 1024 * 1024, encoding: "utf8" }, (error, stdout, stderr) => {
      done({ ok: !error, code: error?.code ?? 0, stdout: stdout ?? "", stderr: stderr ?? "", missing: error?.code === "ENOENT" });
    });
  });
}

const check = (id, status, detail, extra = {}) => ({ id, status, detail, ...extra });

// Generated audit artifacts belong in PR comments/attachments, never in Git.
const EVIDENCE_PATHS = [
  [/^docs\/research\//, "audit evidence under docs/research"],
  [/(^|\/)(report|summary|receipt|issue-\d+|events)\.(json|jsonl|md)$/i, "audit report/receipt file"],
  [/\.artifacts\//, "runner artifact directory"],
  [/\.(png|jpe?g|webm|mp4)$/i, "image/video evidence"],
];
// Product images that legitimately ship with a harness.
const ALLOWED_MEDIA = [/^frontend\/src\/renderer\/assets\//, /^frontend\/src\/landing\/.*\/(assets|images)\//, /^packages\/mobile\/assets\//];

// Code that every harness runs through. A change here needs tests for other
// harnesses (at least codex and claude-code), not only the new one. Pure
// registration files (registries, activity dispatch, catalog dispatcher) are
// expected touchpoints and are deliberately not listed.
const SHARED_CODE = [
  /^backend\/internal\/adapters\/agent\/(observe|terminal|conpty|sessionguard)\//,
  /^backend\/internal\/adapters\/chatdriver\/(acp|nativeacp|persistenthost)\//,
  /^backend\/internal\/(session_manager|sessionguard|lifecycle|conpty|terminal)\//,
  /^backend\/pkg\/agentruntime\//,
  /^frontend\/src\/renderer\/components\/(chat|TaskComposer|SessionView)/,
];

// Failed-CI log signatures and what to do about them.
const CI_SIGNATURES = [
  { kind: "lint", pattern: /golangci|goimports|gofmt|revive|staticcheck|errcheck|gocritic|unparam|File is not properly formatted|ST1005/i,
    action: "Fix the reported lines (gofmt/goimports, exported-comment, errcheck, octal 0o700, error-string case), then push." },
  { kind: "harness_matrix", pattern: /missing explicit refresh capability|refresh capability = |supported harnesses missing from registry|install target count|install inventory|TestMigrateAllowsEveryShippedHarness|harness .* not in/i,
    action: "A main-wide matrix lists every harness: implement the capability or list the harness. Rerun harness_touchpoints.mjs after merging main." },
  { kind: "migration", pattern: /migration|goose|duplicate version|burned/i,
    action: "Renumber the migration above main's latest and any open PR's, and keep the CHECK repair anchors in db.go working." },
  { kind: "live_feed", pattern: /returned status 40[04]|pypi\.org|registry\.npmjs\.org|dist-tags/i,
    action: "Release-feed lookup: strip version specifiers from package names for every manager (npm @x, pip/uv ==x), with a fixture per manager." },
  { kind: "compile", pattern: /cannot use|undefined: |declared and not used|imported and not used|build failed|tsc|TS\d{4}:/,
    action: "Compile error: fix and run `GOWORK=off go build ./...` / `npm run typecheck -w frontend` before pushing." },
  { kind: "known_flake", pattern: /text file busy|ETXTBSY|row\.animate|TestKillReq|autoreview timeout/i,
    action: "Known flake unrelated to the harness: rerun the job once (`gh run rerun --failed`). A second failure is real." },
];

export function classifyCiLog(log) {
  const match = CI_SIGNATURES.find((item) => item.pattern.test(log));
  return match ? { kind: match.kind, action: match.action } : { kind: "test", action: "Read the failing test, reproduce it on the integration branch, fix it with the smallest change." };
}

export function evidenceViolations(files) {
  return files.flatMap((file) => {
    if (ALLOWED_MEDIA.some((pattern) => pattern.test(file))) return [];
    const hit = EVIDENCE_PATHS.find(([pattern]) => pattern.test(file));
    return hit ? [{ file, why: hit[1] }] : [];
  });
}

export function sharedChanges(files) {
  return files.filter((file) => SHARED_CODE.some((pattern) => pattern.test(file)) && !file.endsWith("_test.go") && !/\.test\.tsx?$/.test(file));
}

export function migrationProblems(added, baseMigrations) {
  const number = (name) => Number(name.match(/^(\d+)_/)?.[1] ?? NaN);
  const baseNumbers = new Map(baseMigrations.map((name) => [number(name), name]));
  const baseMax = Math.max(0, ...baseNumbers.keys());
  const problems = [];
  for (const name of added) {
    const value = number(name);
    if (!Number.isFinite(value)) continue;
    if (baseNumbers.has(value)) problems.push({ file: name, why: `number ${value} already used on base by ${baseNumbers.get(value)}` });
    else if (value <= baseMax && String(value).length < 10) problems.push({ file: name, why: `number ${value} is below base's latest ${baseMax}; renumber after it (or use the timestamp generator)` });
  }
  return problems;
}

export async function prCheck({ repo, base, pr = null }) {
  const cwd = resolve(repo);
  const checks = [];
  const mergeBase = await run("git", ["merge-base", "HEAD", base], { cwd });
  if (!mergeBase.ok) return { checks: [check("base", "FAIL", `cannot find merge base with ${base}: ${mergeBase.stderr.trim()}`)] };
  const fork = mergeBase.stdout.trim();
  const diff = await run("git", ["diff", "--name-status", "--no-renames", `${fork}...HEAD`], { cwd });
  const entries = diff.stdout.trim().split("\n").filter(Boolean).map((line) => { const [status, file] = line.split("\t"); return { status, file }; });
  const changed = entries.filter((entry) => entry.status !== "D").map((entry) => entry.file);

  const evidence = evidenceViolations(changed);
  checks.push(check("no_committed_evidence", evidence.length ? "FAIL" : "PASS",
    evidence.length ? `${evidence.length} audit/evidence file(s) are in the diff; move them to PR comments/attachments and \`git rm\` them` : "no audit evidence in the diff",
    { files: evidence }));

  const workflows = entries.filter((entry) => entry.file.startsWith(".github/workflows/"));
  checks.push(check("no_workflow_changes", workflows.length ? "FAIL" : "PASS",
    workflows.length ? `workflow changes are out of scope for a harness PR: ${workflows.map((entry) => entry.file).join(", ")}` : "no workflow changes",
    { files: workflows.map((entry) => entry.file) }));

  const patch = await run("git", ["diff", "-U0", `${fork}...HEAD`, "--", ...changed.filter((file) => !/\.(png|jpe?g|svg|ico)$/i.test(file))], { cwd });
  const leaks = [...patch.stdout.matchAll(/^\+.*?((?:\/home\/[a-z][\w-]*|\/Users\/[A-Za-z][\w.-]*)\/[^\s"'`)]*)/gm)].map((match) => match[1]);
  checks.push(check("no_host_paths", leaks.length ? "FAIL" : "PASS",
    leaks.length ? `added lines contain host-specific paths, e.g. ${leaks.slice(0, 3).join(", ")}` : "no host-specific absolute paths added", { samples: leaks.slice(0, 10) }));

  const whitespace = await run("git", ["diff", "--check", `${fork}...HEAD`], { cwd });
  checks.push(check("diff_check", whitespace.ok ? "PASS" : "FAIL", whitespace.ok ? "no whitespace errors" : whitespace.stdout.trim().split("\n").slice(0, 5).join("; ")));

  const goFiles = changed.filter((file) => file.endsWith(".go"));
  if (goFiles.length) {
    const goimports = await run("goimports", ["-l", ...goFiles], { cwd });
    const formatter = goimports.missing ? await run("gofmt", ["-l", ...goFiles], { cwd }) : goimports;
    const unformatted = formatter.stdout.trim().split("\n").filter(Boolean);
    checks.push(check("go_format", formatter.missing ? "NOT_RUN" : unformatted.length ? "FAIL" : "PASS",
      formatter.missing ? "neither goimports nor gofmt is installed"
        : unformatted.length ? `run ${goimports.missing ? "gofmt" : "goimports"} -w on: ${unformatted.join(", ")}` : `${goFiles.length} changed Go file(s) are formatted${goimports.missing ? " (gofmt only; goimports not installed)" : ""}`,
      { files: unformatted }));
  }

  const migrationDir = "backend/internal/storage/sqlite/migrations/";
  const addedMigrations = entries.filter((entry) => entry.status === "A" && entry.file.startsWith(migrationDir)).map((entry) => entry.file.slice(migrationDir.length));
  if (addedMigrations.length) {
    const baseList = await run("git", ["ls-tree", "--name-only", `${base}:${migrationDir}`], { cwd });
    const problems = migrationProblems(addedMigrations, baseList.stdout.trim().split("\n").filter(Boolean));
    checks.push(check("migration_numbers", problems.length ? "FAIL" : "PASS",
      problems.length ? problems.map((item) => `${item.file}: ${item.why}`).join("; ") : `new migration(s) ${addedMigrations.join(", ")} do not collide with ${base}`, { problems }));
  }

  const shared = sharedChanges(changed);
  // Tests in the Codex/Claude adapters or drivers (codex, codexappserver,
  // claudecode, claudeacp) prove the shared change did not move them.
  const crossTests = changed.filter((file) => /\/(codex\w*|claude\w*)\/[^/]*_test\.go$/.test(file));
  checks.push(check("shared_code_blast_radius", shared.length && !crossTests.length ? "WARN" : "PASS",
    shared.length ? `${shared.length} shared file(s) changed (${shared.slice(0, 4).join(", ")}${shared.length > 4 ? ", …" : ""})${crossTests.length ? "; cross-harness tests changed too" : "; add or update tests proving codex and claude-code still behave the same"}` : "no shared harness code changed",
    { files: shared }));

  const behind = await run("git", ["rev-list", "--count", `HEAD..${base}`], { cwd });
  const count = Number(behind.stdout.trim() || 0);
  checks.push(check("base_freshness", count > 50 ? "WARN" : "PASS",
    count ? `${count} commit(s) behind ${base}; merge it, then rerun harness_touchpoints.mjs and this check (main adds per-harness matrices)` : `up to date with ${base}`, { behind: count }));

  if (pr) checks.push(...await ciChecks({ cwd, pr }));
  return { base, mergeBase: fork, changedFiles: changed.length, checks };
}

// Reads exact-head CI and, for each failed job, the tail of its failed log
// with a classification. This replaces posting "waiting for CI" again.
async function ciChecks({ cwd, pr }) {
  const listed = await run("gh", ["pr", "checks", String(pr), "--json", "name,state,link,bucket"], { cwd });
  if (!listed.ok) return [check("ci", "NOT_RUN", listed.missing ? "gh is not installed" : `gh pr checks failed: ${listed.stderr.trim()}`)];
  const items = JSON.parse(listed.stdout || "[]");
  const pending = items.filter((item) => item.bucket === "pending");
  const failed = items.filter((item) => item.bucket === "fail");
  if (!failed.length) {
    return [check("ci", pending.length ? "PENDING" : "PASS", pending.length ? `${pending.length} check(s) still running; poll again later instead of posting a status comment` : `${items.length} check(s) passed`)];
  }
  const results = [];
  for (const item of failed) {
    const runId = item.link?.match(/\/runs\/(\d+)/)?.[1];
    const log = runId ? await run("gh", ["run", "view", runId, "--log-failed"], { cwd, timeoutMs: 120_000 }) : { stdout: "" };
    const tail = log.stdout.split("\n").filter((line) => /error|fail|FAIL|panic|--- FAIL|::error/i.test(line)).slice(-25);
    const verdict = classifyCiLog(log.stdout);
    results.push(check(`ci:${item.name}`, "FAIL", `${verdict.kind}: ${verdict.action}`, { link: item.link, logTail: tail.map((line) => line.slice(0, 400)) }));
  }
  return results;
}

export function formatPrCheck(result) {
  const lines = [`Harness PR check against ${result.base} (${result.changedFiles ?? 0} changed files)`];
  for (const item of result.checks) {
    lines.push(`  [${item.status.padEnd(7)}] ${item.id}: ${item.detail}`);
    for (const line of item.logTail ?? []) lines.push(`             ${line}`);
  }
  return `${lines.join("\n")}\n`;
}

const HELP = `Usage: node scripts/harness_pr_check.mjs [--repo PATH] [--base REF] [--pr NUMBER] [--json]

Checks a harness integration branch before pushing and after merging main:
committed evidence, workflow changes, host paths, whitespace, Go formatting,
migration numbers, shared-code blast radius, drift behind base and, with --pr,
exact-head CI with classified failed-log tails. Exit 1 on any FAIL.`;

async function main(argv = process.argv.slice(2)) {
  const options = { repo: process.cwd(), base: "origin/main", pr: null, json: false };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--json") options.json = true;
    else if (argument === "-h" || argument === "--help") { console.log(HELP); return 0; }
    else if (["--repo", "--base", "--pr"].includes(argument) && argv[index + 1] !== undefined) options[argument.slice(2)] = argv[++index];
    else { console.error(`error: unknown or incomplete option ${argument}`); return 2; }
  }
  const result = await prCheck(options);
  process.stdout.write(options.json ? `${JSON.stringify(result, null, 2)}\n` : formatPrCheck(result));
  return result.checks.some((item) => item.status === "FAIL") ? 1 : 0;
}

const invokedAsScript = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedAsScript) process.exitCode = await main();
