// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { launchTriageInTerminal, type TriageInput } from "./triage-launch";
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
	it.each([undefined, "claude", "codex"] as const)("launches the bundled ao triage with agent %s", async (agent) => {
		expect(await launchTriageInTerminal({ agent }, { AO_DATA_DIR: "/home/test/.ao/dev/data", AO_RUN_FILE: "/home/test/.ao/dev/running.json" })).toEqual({ ok: true });
		expect(terminalCommand).toHaveBeenCalledWith([
			"/usr/bin/env", "AO_DATA_DIR=/home/test/.ao/dev/data", "AO_RUN_FILE=/home/test/.ao/dev/running.json",
			"/bundled resources/daemon/ao", "triage", ...(agent ? ["--agent", agent] : []),
		], "darwin", expect.any(Object), expect.any(Function));
		expect(launchCommand).toHaveBeenCalledWith("/usr/bin/osascript", ["-e", "script"], expect.any(String));
	});

	it.each(["claude; touch /tmp/no", "", null, 3])("rejects invalid agent %s", async (agent) => {
		expect((await launchTriageInTerminal({ agent } as TriageInput)).ok).toBe(false);
		expect(terminalCommand).not.toHaveBeenCalled();
	});

	it("returns a copyable command with state overrides when no terminal exists", async () => {
		vi.mocked(terminalCommand).mockReturnValue(undefined);
		expect(await launchTriageInTerminal({ agent: "codex" }, { AO_DATA_DIR: "/data with spaces", AO_RUN_FILE: "/data/run.json" })).toEqual({
			ok: false, error: "No terminal was found.\nRun this command in a terminal:\nAO_DATA_DIR='/data with spaces' AO_RUN_FILE='/data/run.json' '/bundled resources/daemon/ao' 'triage' '--agent' 'codex'",
		});
		const result = await launchTriageInTerminal({}, { AO_DATA_DIR: "/data's" });
		expect(result).toMatchObject({ error: expect.stringContaining("AO_DATA_DIR='/data'\\''s'") });
	});

	it("returns the error when spawning fails", async () => {
		vi.mocked(launchCommand).mockRejectedValue(new Error("Terminal unavailable"));
		expect(await launchTriageInTerminal({ agent: "codex" })).toEqual({ ok: false, error: expect.stringContaining("Terminal unavailable\nRun this command in a terminal:\nAO_DATA_DIR=") });
	});

	it("returns the exact PowerShell command when a Windows terminal fails", async () => {
		vi.stubGlobal("process", { ...process, platform: "win32" });
		vi.mocked(launchCommand).mockRejectedValue(new Error("Terminal unavailable"));
		const result = await launchTriageInTerminal({ agent: "claude" }, { AO_DATA_DIR: "C:\\Users\\%name%\\.ao" });
		const argv = vi.mocked(terminalCommand).mock.calls[0][0];
		expect(result).toEqual({ ok: false, error: `Terminal unavailable\nRun this command in a terminal:\n${Buffer.from(argv[3], "base64").toString("utf16le")}` });
	});

	it("encodes Windows paths before cmd expansion", async () => {
		vi.stubGlobal("process", { ...process, platform: "win32" });
		await launchTriageInTerminal({ agent: "claude" }, { AO_DATA_DIR: "C:\\Users\\%name%\\.ao" });
		const argv = vi.mocked(terminalCommand).mock.calls[0][0];
		expect(argv.slice(1, 3)).toEqual(["-NoProfile", "-EncodedCommand"]);
		expect(Buffer.from(argv[3], "base64").toString("utf16le")).toContain("C:\\Users\\%name%\\.ao");
		expect(Buffer.from(argv[3], "base64").toString("utf16le")).toContain("'triage' '--agent' 'claude'");
	});
});
