#!/usr/bin/env node
// DevCo evidence collector (Sprint 0 draft). Runs as a Cezar check step inside the task worktree.
// Writes .devco/evidence/<run-id>/{index.json,checks.txt,diffstat.txt}. Exits non-zero when the
// checks fail, so a red change can never reach the commit / draft PR nodes.
// Zero dependencies: Node >= 20 built-ins only.
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const env = process.env;
const runId = env.CEZ_RUN_ID || `local-${new Date().toISOString().replace(/[:.]/g, '-')}`;
// CEZ_BASE is the run's base branch NAME, which may be local (main) or remote-tracking
// (origin/main). The evidence records the fork-point COMMIT, so every run reads the same way.
const baseRef = env.CEZ_BASE || 'HEAD';
const outDir = join('.devco', 'evidence', runId);
mkdirSync(outDir, { recursive: true });

const git = (...args) => execFileSync('git', args, { encoding: 'utf8' }).trim();
const base = git('merge-base', 'HEAD', baseRef);

// 1. Deterministic checks, re-run here so the evidence is the real output, not a summary.
const checks = spawnSync('npm', ['run', '-s', 'check'], { encoding: 'utf8' });
const checksText = `$ npm run -s check\n${checks.stdout}${checks.stderr}\nexit=${checks.status}\n`;
writeFileSync(join(outDir, 'checks.txt'), checksText);

// 2. Diff against the recorded fork point, working tree included (the commit node runs later).
//    The evidence folder itself is excluded so the numbers describe the product change only, and
//    so is the agent's PR summary (`.devco/pr-summary.md`, any case): Cezar copies it into the PR
//    description and never commits it, so listing it would name a file the branch does not have.
const pathspec = ['--', '.', ':(exclude).devco/evidence', ':(top,exclude,icase,literal).devco/pr-summary.md'];
const diffstat = git('diff', '--stat', base, ...pathspec);
const numstat = git('diff', '--numstat', base, ...pathspec);
const untracked = git('ls-files', '--others', '--exclude-standard', ...pathspec);
writeFileSync(join(outDir, 'diffstat.txt'), `${diffstat}\n\nuntracked:\n${untracked}\n`);
let insertions = 0;
let deletions = 0;
const files = [];
for (const line of numstat.split('\n').filter(Boolean)) {
  const [add, del, file] = line.split('\t');
  insertions += Number(add) || 0;
  deletions += Number(del) || 0;
  files.push(file);
}
for (const file of untracked.split('\n').filter(Boolean)) files.push(file);

const index = {
  schema: 'devco.evidence/v0',
  runId,
  projectId: env.CEZ_PROJECT_ID ?? null,
  branch: env.CEZ_BRANCH ?? git('rev-parse', '--abbrev-ref', 'HEAD'),
  base,
  baseRef,
  attempt: env.CEZ_ATTEMPT ? Number(env.CEZ_ATTEMPT) : null,
  collectedAt: new Date().toISOString(),
  node: process.version,
  checks: { command: 'npm run -s check', exitCode: checks.status, file: 'checks.txt' },
  diff: { files, insertions, deletions, file: 'diffstat.txt' },
  transcript: `Cezar run ${runId} (Cezar state, .ai/cezar/runs/ in the project checkout)`,
  complete: checks.status === 0 && files.length > 0,
};
writeFileSync(join(outDir, 'index.json'), `${JSON.stringify(index, null, 2)}\n`);

// 3. No copy into the main checkout. Collectors before setup v13 mirrored this folder there, and
//    the board still checks that copy first for those older runs. This step runs in Cezar's check
//    sandbox, which writes only inside the worktree, so the copy failed with EPERM; the board then
//    reads this folder from the run's worktree, else from the run's branch (the PR commits it).

console.log(`evidence → ${outDir} (checks exit ${checks.status}, ${files.length} file(s) changed)`);
if (checks.status !== 0) process.exit(1);
if (files.length === 0) {
  console.error('no product change found against the fork point');
  process.exit(2);
}
