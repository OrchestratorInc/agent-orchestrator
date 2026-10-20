// @vitest-environment node
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { spawn } from "node:child_process";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { prepareTriage } from "./triage-launch";

vi.mock("electron", () => ({ app: { isPackaged: true, getAppPath: () => "/app" } }));
vi.mock("node:child_process", () => ({ spawn: vi.fn() }));
const payload = { dir: "/home/test/.ao/triage/1", prompt: "Read /home/test/.ao/triage/1/context.md", context: "/context", report: "/report", playbook: "/playbook" };
let child: EventEmitter & { stdout: PassThrough; kill: ReturnType<typeof vi.fn> };
beforeEach(() => {
	vi.stubGlobal("process", { ...process, platform: "darwin", resourcesPath: "/bundled resources", env: { AO_DAEMON_COMMAND: "stale-ao", PATH: "/bin" } });
	child = Object.assign(new EventEmitter(), { stdout: new PassThrough(), kill: vi.fn() });
	vi.mocked(spawn).mockReturnValue(child as unknown as ReturnType<typeof spawn>);
});
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); vi.resetAllMocks(); });
const prepare = () => prepareTriage({ AO_DATA_DIR: "/home/test/.ao/dev/data", AO_RUN_FILE: "/home/test/.ao/dev/running.json" });

describe("prepareTriage", () => {
	it("runs the bundled binary with the desktop environment and returns only prompt and dir", async () => {
		const result = prepare();
		child.stdout.write(JSON.stringify(payload));
		child.emit("close", 0);
		expect(await result).toEqual({ ok: true, prompt: payload.prompt, dir: payload.dir });
		expect(spawn).toHaveBeenCalledWith("/bundled resources/daemon/ao", ["triage", "--no-launch", "--json"], expect.objectContaining({
			env: expect.objectContaining({ AO_DATA_DIR: "/home/test/.ao/dev/data", AO_RUN_FILE: "/home/test/.ao/dev/running.json", PATH: "/bin" }),
		}));
	});
	it.each(["not json", "null", "[]", ...Object.keys(payload).map((key) => JSON.stringify({ ...payload, [key]: 1 }))])("rejects invalid output %s", async (output) => {
		const result = prepare();
		child.stdout.write(output);
		child.emit("close", 0);
		expect(await result).toEqual({ ok: false, error: expect.any(String) });
	});
	it("rejects a failed command even with valid JSON", async () => {
		const result = prepare(); child.stdout.write(JSON.stringify(payload)); child.emit("close", 1);
		expect((await result).ok).toBe(false);
	});
	it("handles spawn failures", async () => {
		const result = prepare(); child.emit("error", new Error("ENOENT"));
		expect((await result).ok).toBe(false);
	});
	it("handles a synchronous spawn failure", async () => {
		vi.mocked(spawn).mockImplementation(() => { throw new Error("spawn failed"); });
		expect((await prepare()).ok).toBe(false);
	});
	it("caps stdout and kills the child", async () => {
		const result = prepare(); child.stdout.write("x".repeat(1024 * 1024 + 1));
		expect(await result).toEqual({ ok: false, error: "The report response was too large." });
		expect(child.kill).toHaveBeenCalledWith("SIGKILL");
	});
	it("allows collection to finish after its ten second budget and emit JSON", async () => {
		vi.useFakeTimers(); const result = prepare();
		await vi.advanceTimersByTimeAsync(12_000);
		expect(child.kill).not.toHaveBeenCalled();
		child.stdout.write(JSON.stringify(payload)); child.emit("close", 0);
		expect(await result).toEqual({ ok: true, prompt: payload.prompt, dir: payload.dir });
		await vi.advanceTimersByTimeAsync(20_000);
		expect(child.kill).not.toHaveBeenCalled();
	});
	it("times out at twenty seconds without waiting for the child to close", async () => {
		vi.useFakeTimers(); const result = prepare();
		await vi.advanceTimersByTimeAsync(19_999);
		expect(child.kill).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1);
		expect(await result).toEqual({ ok: false, error: "Preparing the report timed out. Try again." });
		expect(child.kill).toHaveBeenCalledWith("SIGKILL");
		child.emit("close", 0);
	});
});
