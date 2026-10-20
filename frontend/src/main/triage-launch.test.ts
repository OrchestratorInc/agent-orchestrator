// @vitest-environment node
import os from "node:os";
import path from "node:path";
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
	it("puts only state overrides and bundled triage in the terminal command", async () => {
		const env = { PATH: "/login/bin", GH_TOKEN: "secret123", AO_DATA_DIR: "/data", AO_RUN_FILE: "/run.json" };
		expect(await launchTriageInTerminal(env)).toEqual({ ok: true });
		expect(launchCommand).toHaveBeenCalledWith(expect.any(String), expect.any(Array), expect.any(String), { env });
		expect(terminalCommand).toHaveBeenCalledWith(["/usr/bin/env", "AO_DATA_DIR=/data", "AO_RUN_FILE=/run.json", "/bundled resources/daemon/ao", "triage"], "darwin", env, expect.any(Function));
	});

	it.each([{}, { AO_DATA_DIR: "", AO_RUN_FILE: "" }, {
		AO_DATA_DIR: path.join(os.homedir(), ".ao", "data"), AO_RUN_FILE: path.join(os.homedir(), ".ao", "running.json"),
	}])("omits state overrides at their defaults: %j", async (env) => {
		vi.mocked(terminalCommand).mockReturnValue(undefined);
		expect(await launchTriageInTerminal(env)).toEqual({ ok: false, error: "report.terminalFailed", command: "'/bundled resources/daemon/ao' 'triage'" });
		expect(terminalCommand).toHaveBeenCalledWith(["/bundled resources/daemon/ao", "triage"], "darwin", env, expect.any(Function));
	});

	it("returns a copyable command with state overrides when no terminal exists", async () => {
		vi.mocked(terminalCommand).mockReturnValue(undefined);
		expect(await launchTriageInTerminal({ AO_DATA_DIR: "/data with spaces", AO_RUN_FILE: "/data/run.json" })).toEqual({
			ok: false, error: "report.terminalFailed", command: "AO_DATA_DIR='/data with spaces' AO_RUN_FILE='/data/run.json' '/bundled resources/daemon/ao' 'triage'",
		});
		const result = await launchTriageInTerminal({ AO_DATA_DIR: "/data's" });
		expect(result).toMatchObject({ command: expect.stringContaining("AO_DATA_DIR='/data'\\''s'") });
	});

	it.each(["darwin", "linux", "win32"] as const)("keeps recovered secrets out of %s command text and errors", async (platform) => {
		vi.stubGlobal("process", { ...process, platform });
		const actual = await vi.importActual<typeof import("./terminal-command")>("./terminal-command");
		vi.mocked(terminalCommand).mockImplementation((argv, target, env) => actual.terminalCommand(argv, target, env, () => true));
		const env = { PATH: "/login/bin", GH_TOKEN: "secret123", ANTHROPIC_API_KEY: "anthropic-test-secret", OPENAI_API_KEY: "openai-test-secret", AO_DATA_DIR: "/data", AO_RUN_FILE: "/run.json" };
		vi.mocked(launchCommand).mockRejectedValue(new Error("GH_TOKEN=secret123"));
		const result = await launchTriageInTerminal(env);
		expect(result).toMatchObject({ ok: false, error: "report.terminalFailed" });
		const argv = vi.mocked(terminalCommand).mock.calls[0][0];
		const commandText = platform === "win32" ? Buffer.from(argv[3], "base64").toString("utf16le") : JSON.stringify(vi.mocked(launchCommand).mock.calls[0][1]);
		for (const value of ["GH_TOKEN", "secret123", "ANTHROPIC_API_KEY", "anthropic-test-secret", "OPENAI_API_KEY", "openai-test-secret", "PATH="]) {
			expect(commandText).not.toContain(value);
			expect(JSON.stringify(result)).not.toContain(value);
		}
		if (platform === "darwin") {
			expect(vi.mocked(launchCommand).mock.calls[0][1]).toEqual(["-e", "tell application \"Terminal\"\nactivate\ndo script \"exec '/usr/bin/env' 'AO_DATA_DIR=/data' 'AO_RUN_FILE=/run.json' '/bundled resources/daemon/ao' 'triage'\"\nend tell"]);
		}
	});
	it("returns the error when spawning fails", async () => {
		vi.mocked(launchCommand).mockRejectedValue(new Error("Terminal unavailable"));
		expect(await launchTriageInTerminal({})).toEqual({ ok: false, error: "report.terminalFailed", command: "'/bundled resources/daemon/ao' 'triage'" });
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
