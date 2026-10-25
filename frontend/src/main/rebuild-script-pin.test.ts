// @vitest-environment node
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

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
