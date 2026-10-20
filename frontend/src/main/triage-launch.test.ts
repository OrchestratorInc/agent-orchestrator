// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { launchTriageInTerminal } from "./triage-launch";
import { terminalCommand } from "./terminal-command";
import { launchCommand } from "./launch-command";

vi.mock("electron", () => ({ app: { isPackaged: true, getAppPath: () => "/app" } }));
vi.mock("./terminal-command", () => ({ terminalCommand: vi.fn() }));
vi.mock("./launch-command", () => ({ launchCommand: vi.fn() }));

afterEach(() => vi.unstubAllGlobals());

beforeEach(() => {
	vi.clearAllMocks();
	vi.stubGlobal("process", { ...process, platform: "darwin", resourcesPath: "/bundled resources", env: { AO_DAEMON_COMMAND: "stale-ao", PATH: "/bin" } });
	vi.mocked(terminalCommand).mockReturnValue({ command: "/usr/bin/osascript", args: ["-e", "script"] });
	vi.mocked(launchCommand).mockResolvedValue(undefined);
});

describe("launchTriageInTerminal", () => {
	it("launches bundled triage with the recovered shell environment", async () => {
 const env = { PATH: "/login/bin", GH_TOKEN: "shell-test-token", AO_DATA_DIR: "/data", AO_RUN_FILE: "/run.json" };
 expect(await launchTriageInTerminal(env)).toEqual({ ok: true });
 expect(launchCommand).toHaveBeenCalledWith(expect.any(String), expect.any(Array), expect.any(String), { env });
 expect(terminalCommand).toHaveBeenCalledWith(["/usr/bin/env", "PATH=/login/bin", "GH_TOKEN=shell-test-token", "AO_DATA_DIR=/data", "AO_RUN_FILE=/run.json", "/bundled resources/daemon/ao", "triage"], "darwin", env, expect.any(Function));
 });

	it("returns a copyable command with state overrides when no terminal exists", async () => {
		vi.mocked(terminalCommand).mockReturnValue(undefined);
		expect(await launchTriageInTerminal({ AO_DATA_DIR: "/data with spaces", AO_RUN_FILE: "/data/run.json" })).toEqual({
			ok: false, error: "report.terminalFailed", command: "AO_DATA_DIR='/data with spaces' AO_RUN_FILE='/data/run.json' '/bundled resources/daemon/ao' 'triage'",
		});
		const result = await launchTriageInTerminal({ AO_DATA_DIR: "/data's" });
		expect(result).toMatchObject({ command: expect.stringContaining("AO_DATA_DIR='/data'\\''s'") });
	});

	it("keeps shell credentials out of the manual fallback", async () => {
 vi.mocked(terminalCommand).mockReturnValue(undefined);
 const result = await launchTriageInTerminal({ GH_TOKEN: "shell-test-token", PATH: "/login/bin" });
 expect(result).toMatchObject({ ok: false, error: "report.terminalFailed" });
 if (!result.ok) { expect(result.command).not.toContain("shell-test-token"); expect(result.command).toContain("PATH='/login/bin'"); }
 });
	it("returns the error when spawning fails", async () => {
		vi.mocked(launchCommand).mockRejectedValue(new Error("Terminal unavailable"));
		expect(await launchTriageInTerminal({})).toEqual({ ok: false, error: "report.terminalFailed", command: expect.stringContaining("AO_DATA_DIR=") });
	});

	it("returns the exact PowerShell command when a Windows terminal fails", async () => {
		vi.stubGlobal("process", { ...process, platform: "win32" });
		vi.mocked(launchCommand).mockRejectedValue(new Error("Terminal unavailable"));
		const result = await launchTriageInTerminal({ AO_DATA_DIR: "C:\\Users\\%name%\\.ao" });
		const argv = vi.mocked(terminalCommand).mock.calls[0][0];
		expect(result).toEqual({ ok: false, error: "report.terminalFailed", command: Buffer.from(argv[3], "base64").toString("utf16le") });
	});

	it("encodes Windows paths before cmd expansion", async () => {
		vi.stubGlobal("process", { ...process, platform: "win32" });
		await launchTriageInTerminal({ AO_DATA_DIR: "C:\\Users\\%name%\\.ao" });
		const argv = vi.mocked(terminalCommand).mock.calls[0][0];
		expect(argv.slice(1, 3)).toEqual(["-NoProfile", "-EncodedCommand"]);
		expect(Buffer.from(argv[3], "base64").toString("utf16le")).toContain("C:\\Users\\%name%\\.ao");
		expect(Buffer.from(argv[3], "base64").toString("utf16le")).toContain("'triage'");
	});
});
