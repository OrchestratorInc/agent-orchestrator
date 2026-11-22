#!/usr/bin/env node
// DevCo PreToolUse guard (Sprint 0, S0-10). Second line of defence behind Cezar's bashAllowlist.
// Claude Code passes the tool call as JSON on stdin; exit code 2 blocks it and shows stderr to
// the agent. Zero dependencies. Regexes are built from parts so this file never matches the
// sandbox secret scanner.
import { readFileSync, realpathSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';

let call;
try {
  call = JSON.parse(readFileSync(0, 'utf8'));
} catch {
  console.error('devco guard: unreadable tool call, blocking');
  process.exit(2);
}
const tool = call.tool_name ?? '';
const input = call.tool_input ?? {};
const block = (why) => {
  console.error(`devco guard blocked ${tool}: ${why}`);
  process.exit(2);
};

const DENY_BASH = [
  [/\bgit\s+push\b[^\n]*(\s--force\b|\s-f\b|\s--force-with-lease\b)/, 'force push'],
  [/\bgit\s+push\b[^\n]*\b(main|master)\b/, 'push to a protected branch'],
  [/\bgh\s+pr\s+(merge|ready)\b/, 'merging or un-drafting a PR (CTO only)'],
  [/\bgh\s+(repo\s+(delete|edit|create)|secret|auth|api)\b/, 'gh admin/API command'],
  [/\b(sudo|su)\b/, 'privilege escalation'],
  [/\brm\s+-[a-z]*r[a-z]*f?\s+(\/|~|\$HOME)(\s|$)/, 'recursive delete of / or home'],
  [/\b(curl|wget)\b[^\n|]*\|\s*(ba|z)?sh\b/, 'pipe download to shell'],
  [/\b(printenv|env)\s*($|\|)|\bset\s*$/, 'environment dump'],
  [/\bsecurity\s+(find|dump)-/, 'keychain read'],
  [/(~|\$HOME)\/\.(ssh|aws|config\/gh|claude)\b/, 'reading credential folders'],
  [/\bnpm\s+(publish|login|adduser|token)\b/, 'npm publish/auth'],
  [/\bchmod\s+[0-7]*777\b/, 'world-writable permissions'],
  // git options that read or write arbitrary files or run programs (review round 4, B2). Confined
  // agents have no shell at all; this is defence in depth for any session that still has one.
  [/\bgit\b[^\n]*\s--output(=|\s|$)/, 'git --output (writes an arbitrary file)'],
  [/\bgit\b[^\n]*\s--no-index\b/, 'git --no-index (reads files outside the repository)'],
  [/\bgit\b[^\n]*\s--ext-diff\b/, 'git --ext-diff (runs an external program)'],
  [/\bgit\b[^\n]*\s--textconv\b/, 'git --textconv (runs a conversion program)'],
  [/\bgit\b[^\n]*\s-O\S*/, 'git -O (reads an order file / opens files in a pager)'],
  [/\bgit\b[^\n]*\s--open-files-in-pager\b/, 'git --open-files-in-pager (runs a program)'],
  [/\bgit\b[^\n]*\s--exec(-path)?\b/, 'git --exec (runs a program)'],
  [/\bgit\b[^\n]*\s(-c\s*\S|--config(-env)?\b)/, 'git -c / --config (overrides configuration, e.g. a pager or diff driver)'],
];

const PROTECTED_PATHS = [/^\.claude(\/|$)/, /^\.ai(\/|$)/, /^scripts(\/|$)/, /^\.devco\/scripts(\/|$)/, /^\.git(\/|$)/, /^\.github(\/|$)/];

/** The real path of `path`, resolving symlinks in the deepest part of it that exists. */
function realish(path) {
  let head = path;
  const tail = [];
  for (;;) {
    try {
      return [realpathSync(head), ...tail.reverse()].join(sep);
    } catch {
      const up = dirname(head);
      if (up === head) return path;
      tail.push(head.slice(up.length + (up.endsWith(sep) ? 0 : 1)));
      head = up;
    }
  }
}

const p = (s) => s; // keeps the parts below readable
const SECRET_SHAPES = [
  new RegExp(p('\\b(gh') + p('p|gho|ghu|ghs|ghr)_') + '[A-Za-z0-9]{36,}'),
  new RegExp(p('github') + p('_pat_') + '[A-Za-z0-9_]{22,}'),
  new RegExp(p('\\bAK') + p('IA') + '[0-9A-Z]{16}\\b'),
  new RegExp(p('\\bsk') + p('-') + '[A-Za-z0-9_-]{20,}'),
  new RegExp(p('-----BEGIN ') + '(?:[A-Z]+ )?' + p('PRIVATE ') + 'KEY-----'),
];

if (tool === 'Bash') {
  const cmd = String(input.command ?? '');
  for (const [re, why] of DENY_BASH) if (re.test(cmd)) block(why);
  for (const re of SECRET_SHAPES) if (re.test(cmd)) block('secret-shaped value in a command');
  process.exit(0);
}

if (['Write', 'Edit', 'MultiEdit', 'NotebookEdit'].includes(tool)) {
  // Normalise before matching (review round 3, M1): `a/../.claude/x`, `./.claude//x` and a
  // symlinked parent all resolve to the same real path, which must stay inside the worktree.
  const root = realish(resolve(process.env.CLAUDE_PROJECT_DIR || process.cwd()));
  const raw = String(input.file_path ?? input.notebook_path ?? '');
  if (!raw) block('no file path');
  const target = realish(resolve(root, raw));
  const rel = relative(root, target);
  if (rel === '' || rel.startsWith('..') || isAbsolute(rel)) block(`write outside the worktree (${raw})`);
  const relPosix = rel.split(sep).join('/');
  for (const re of PROTECTED_PATHS) if (re.test(relPosix)) block(`${relPosix} is owned by QA/DevOps, not the implementing agent`);
  const text = [input.content, input.new_string, input.new_source, ...(input.edits ?? []).map((e) => e.new_string)]
    .filter(Boolean)
    .join('\n');
  for (const re of SECRET_SHAPES) if (re.test(text)) block('secret-shaped value in file content');
}
process.exit(0);
