// @vitest-environment node
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";

const script = fileURLToPath(new URL("./move-markdown.mjs", import.meta.url));
const roots: string[] = [];

afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

/** Runs a copy of the script from a checkout whose path is `name`. */
function runFrom(name: string) {
  const root = join(mkdtempSync(join(tmpdir(), "docs-")), name);
  roots.push(root);
  mkdirSync(join(root, "scripts"), { recursive: true });
  mkdirSync(join(root, "out", "markdown"), { recursive: true });
  copyFileSync(script, join(root, "scripts", "move-markdown.mjs"));
  writeFileSync(join(root, "out", "markdown", "quickstart.md"), "# Quickstart\n");
  execFileSync(process.execPath, [join(root, "scripts", "move-markdown.mjs")]);
  return join(root, "out");
}

describe("move-markdown", () => {
  it.each(["checkout", "AO review"])("moves the Markdown pages to the site root from %s", (name) => {
    const out = runFrom(name);
    expect(readFileSync(join(out, "quickstart.md"), "utf8")).toBe("# Quickstart\n");
    expect(existsSync(join(out, "markdown"))).toBe(false);
  });
});
