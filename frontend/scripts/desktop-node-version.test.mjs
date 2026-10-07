import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const root = new URL("../../", import.meta.url);
const build = readFileSync(new URL(".github/workflows/build-artifacts.yml", root), "utf8");
const frontend = readFileSync(new URL(".github/workflows/frontend.yml", root), "utf8");
const command = build.match(/run: (node -p .+)/)?.[1];

test("frontend CI and desktop builds use the declared Node version", () => {
	assert.equal(typeof JSON.parse(readFileSync(new URL("frontend/package.json", root))).engines.node, "string");
	assert.equal(
		frontend.match(/node-version-file: frontend\/package\.json/g)?.length,
		frontend.match(/uses: actions\/setup-node@/g)?.length,
	);
	assert.match(build, /node-version: \$\{\{ steps\.node\.outputs\.version \}\}/);
});

for (const version of ["24.x", "26.x", undefined]) {
	test(`desktop build resolves ${version ?? "legacy ref"}`, () => {
		assert.ok(command, "desktop build must read its Node version");
		const directory = mkdtempSync(join(tmpdir(), "desktop-node-"));
		try {
			writeFileSync(join(directory, "package.json"), JSON.stringify(version ? { engines: { node: version } } : {}));
			const output = join(directory, "output");
			execFileSync("bash", ["-e", "-c", command], { cwd: directory, env: { ...process.env, GITHUB_OUTPUT: output } });
			assert.equal(readFileSync(output, "utf8"), `version=${version ?? "24"}\n`);
		} finally {
			rmSync(directory, { recursive: true, force: true });
		}
	});
}
