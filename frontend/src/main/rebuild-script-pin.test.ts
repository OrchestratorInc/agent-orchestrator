// @vitest-environment node
import { describe, it, expect } from "vitest";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

// rebuild-ao.sh must pin node to an nvm-managed v20/v22 before npm and
// electron-forge run: brew node 26 silently truncates `forge package`
// (exit 0, empty out/, the failure only surfaces later at ditto). The
// 2026-10-25 script rewrite dropped the pin and the build "succeeded" with no
// app bundle. This guard is textual on purpose — the regression class is
// exactly "a rewrite loses the pin", so it reads the script and fails when
// the pin is gone. Keep the selector below in sync with the pin block.
describe("scripts/rebuild-ao.sh node pin", () => {
	const scriptPath = fileURLToPath(new URL("../../../scripts/rebuild-ao.sh", import.meta.url));
	const script = () => readFileSync(scriptPath, "utf8");

	it("selects node from nvm v20/v22 version dirs, never bare system node", () => {
		const body = script();
		expect(body).toMatch(/versions\/node\/v20\./);
		expect(body).toMatch(/versions\/node\/v22\./);
	});

	it("prepends the chosen nvm dir to PATH", () => {
		expect(script()).toMatch(/export PATH="\$\{node_dir\}\/bin:\$\{PATH\}"/);
	});

	it("fails loudly when no nvm LTS is installed instead of falling back to system node", () => {
		const body = script();
		expect(body).toMatch(/exit 1/);
		expect(body).toMatch(/nvm node v20\/v22 not found/);
	});
});

// The sh pin only covers rebuild-ao.sh. Every OTHER path into packaging — a
// manual `npm run package` from frontend/ — must fail loudly too, because
// electron-forge under brew node 26 does not error: it exits 0 with an empty
// out/ and the breakage surfaces only at the later ditto step. The prepackage
// guard (scripts/check-package-node.mjs) is that choke point; these tests run
// it under the machine's REAL node binaries, so the brew-26 rejection is
// exercised against the actual offending interpreter.
describe("prepackage node guard", () => {
	const guardPath = fileURLToPath(new URL("../../scripts/check-package-node.mjs", import.meta.url));

	const nvmV20 = (() => {
		const base = join(homedir(), ".nvm", "versions", "node");
		if (!existsSync(base)) return "";
		const dir = readdirSync(base).filter((name) => /^v20\./.test(name)).sort().at(-1);
		return dir ? join(base, dir, "bin", "node") : "";
	})();
	const brewNode = "/opt/homebrew/bin/node";

	it("is wired into the prepackage npm script", () => {
		const pkg = JSON.parse(readFileSync(
			fileURLToPath(new URL("../../../frontend/package.json", import.meta.url)), "utf8",
		)) as { scripts?: Record<string, string> };
		expect(pkg.scripts?.prepackage ?? "").toMatch(/check-package-node\.mjs/);
	});

	it("rejects node 26 (brew) with a loud message instead of a silent empty package", () => {
		if (!existsSync(brewNode)) return; // hosted CI has no brew node
		const run = spawnSync(brewNode, [guardPath], { encoding: "utf8" });
		expect(run.status).not.toBe(0);
		expect(run.stderr).toMatch(/not supported for packaging/);
	});

	it("accepts nvm v20", () => {
		if (!nvmV20 || !existsSync(nvmV20)) return; // hosted CI has no nvm dir
		const run = spawnSync(nvmV20, [guardPath], { encoding: "utf8" });
		expect(run.status).toBe(0);
	});
});
