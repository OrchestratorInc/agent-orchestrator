import { describe, expect, it } from "vitest";
import { removalCommand } from "./HarnessUninstallGuide";

describe("removalCommand", () => {
	it("keeps ~ and globs unquoted so the shell expands them", () => {
		expect(removalCommand(["~/.local/bin/muse", "~/.local/bin/muse-bin-*"])).toBe("rm -rf ~/.local/bin/muse ~/.local/bin/muse-bin-*");
	});

	it("quotes POSIX paths with spaces", () => {
		expect(removalCommand(["/Applications/Kiro CLI.app"])).toBe('rm -rf "/Applications/Kiro CLI.app"');
	});

	it("uses PowerShell with expanded environment variables for Windows paths", () => {
		expect(removalCommand([String.raw`%USERPROFILE%\.local\bin\claude.exe`, String.raw`%LOCALAPPDATA%\cursor-agent`]))
			.toBe(String.raw`Remove-Item -Recurse -Force "$env:USERPROFILE\.local\bin\claude.exe"; Remove-Item -Recurse -Force "$env:LOCALAPPDATA\cursor-agent"`);
	});

	it("has nothing to run without paths", () => {
		expect(removalCommand([])).toBe("");
	});
});
