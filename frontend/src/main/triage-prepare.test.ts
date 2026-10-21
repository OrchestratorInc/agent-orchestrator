// @vitest-environment node
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { spawn } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { app } from "electron";
import { buildDaemonEnv } from "../shared/shell-env";
import { prepareTriage } from "./triage-launch";

vi.mock("electron", () => ({ app: { get isPackaged() { return true; }, getAppPath: () => "/app" } }));
vi.mock("node:child_process", () => ({ spawn: vi.fn() }));
const payload = { dir: "/home/test/.ao/triage/1", prompt: "Read /home/test/.ao/triage/1/context.md", context: "/context", report: "/report", playbook: "/playbook" };
let child: EventEmitter & { stdout: PassThrough; stderr: PassThrough; kill: ReturnType<typeof vi.fn> };
let binaryDir: string;
beforeEach(() => {
	vi.stubGlobal("process", { ...process, platform: "darwin", resourcesPath: "/bundled resources", env: { AO_DAEMON_COMMAND: "stale-ao", PATH: "/bin" } });
	binaryDir = mkdtempSync(path.join(os.tmpdir(), "triage-prepare-"));
	child = Object.assign(new EventEmitter(), { stdout: new PassThrough(), stderr: new PassThrough(), kill: vi.fn() });
	vi.mocked(spawn).mockReturnValue(child as unknown as ReturnType<typeof spawn>);
	vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => { rmSync(binaryDir, { recursive: true, force: true }); vi.unstubAllGlobals(); vi.useRealTimers(); vi.restoreAllMocks(); vi.resetAllMocks(); });
const prepare = () => prepareTriage({ ...buildDaemonEnv(process.env, { PATH: "/login/bin", GH_TOKEN: "shell-test-token" }, {}), AO_DATA_DIR: "/home/test/.ao/dev/data", AO_RUN_FILE: "/home/test/.ao/dev/running.json" });

describe("prepareTriage", () => {
	it("runs the bundled binary with the desktop environment and returns only prompt and dir", async () => {
		const result = prepare();
		child.stdout.write(JSON.stringify(payload));
		child.emit("close", 0);
		expect(await result).toEqual({ ok: true, prompt: payload.prompt, dir: payload.dir });
		expect(spawn).toHaveBeenCalledWith("/bundled resources/daemon/ao", ["triage", "--no-launch", "--json"], expect.objectContaining({
			env: expect.objectContaining({ AO_DATA_DIR: "/home/test/.ao/dev/data", AO_RUN_FILE: "/home/test/.ao/dev/running.json", PATH: expect.stringContaining("/login/bin"), GH_TOKEN: "shell-test-token" }),
		}));
	});
	it("uses a trimmed absolute executable override without a built dev binary", async () => {
		const binary = path.join(binaryDir, "ao with spaces");
		writeFileSync(binary, "", { mode: 0o755 });
		vi.spyOn(app, "isPackaged", "get").mockReturnValue(false);
		const result = prepareTriage({ AO_DAEMON_COMMAND: ` ${binary} ` });
		child.stdout.write(JSON.stringify(payload)); child.emit("close", 0);
		expect(await result).toMatchObject({ ok: true });
		expect(spawn).toHaveBeenCalledWith(binary, ["triage", "--no-launch", "--json"], expect.not.objectContaining({ shell: true }));
	});
	it.each(["shell expression", "missing file", "directory", "PATH command"])("ignores a %s override", async (kind) => {
		const binary = path.join(binaryDir, "ao");
		writeFileSync(binary, "", { mode: 0o755 });
		const override = kind === "shell expression" ? `${binary} daemon` : kind === "directory" ? binaryDir : kind === "PATH command" ? "ao" : path.join(binaryDir, "missing");
		const result = prepareTriage({ AO_DAEMON_COMMAND: override });
		child.stdout.write(JSON.stringify(payload)); child.emit("close", 0);
		expect(await result).toMatchObject({ ok: true });
		expect(spawn).toHaveBeenCalledWith("/bundled resources/daemon/ao", expect.any(Array), expect.not.objectContaining({ shell: true }));
	});
	it.skipIf(process.platform === "win32")("ignores a non-executable file override", async () => {
		const binary = path.join(binaryDir, "ao");
		writeFileSync(binary, ""); chmodSync(binary, 0o644);
		const result = prepareTriage({ AO_DAEMON_COMMAND: binary });
		child.stdout.write(JSON.stringify(payload)); child.emit("close", 0);
		expect(await result).toMatchObject({ ok: true });
		expect(spawn).toHaveBeenCalledWith("/bundled resources/daemon/ao", expect.any(Array), expect.any(Object));
	});
	it.each(["not json", "null", "[]", ...Object.keys(payload).map((key) => JSON.stringify({ ...payload, [key]: 1 }))])("rejects invalid output %s", async (output) => {
		const result = prepare();
		child.stderr.write("invalid response diagnostic");
		child.stdout.write(output);
		child.emit("close", 0);
		expect(await result).toEqual({ ok: false, error: expect.any(String) });
		expect(console.error).toHaveBeenCalledExactlyOnceWith("Report a problem prepare failed: invalid JSON response; stderr: invalid response diagnostic");
	});
	it("rejects a failed command even with valid JSON", async () => {
		const result = prepare(); child.stderr.write("could not collect diagnostics\ncheck permissions\n"); child.stdout.write(JSON.stringify(payload)); child.emit("close", 1);
		expect((await result).ok).toBe(false);
		expect(console.error).toHaveBeenCalledExactlyOnceWith("Report a problem prepare failed: exit 1; stderr: could not collect diagnostics check permissions");
	});
	it("handles spawn failures", async () => {
		const result = prepare(); child.stderr.write("spawn diagnostic"); child.emit("error", new Error("ENOENT")); child.emit("close", -1);
		expect((await result).ok).toBe(false);
		expect(console.error).toHaveBeenCalledExactlyOnceWith("Report a problem prepare failed: spawn error; stderr: spawn diagnostic");
	});
	it("keeps only the last 8 KiB of stderr", async () => {
		const result = prepare();
		child.stderr.write(`discarded diagnostic${"x".repeat(8192)}`);
		child.stderr.write("\nlast failure\n"); child.emit("close", 1);
		await result;
		expect(console.error).toHaveBeenCalledOnce();
		const line = vi.mocked(console.error).mock.calls[0][0] as string;
		expect(line).not.toContain("discarded diagnostic");
		expect(line).not.toContain("\n");
		expect(line).toMatch(/last failure$/);
		expect(Buffer.byteLength(line)).toBeLessThan(8300);
	});
	it("keeps credentials in the spawn environment and out of returned results and logs", async () => {
		const logs = [vi.spyOn(console, "log"), vi.spyOn(console, "info"), vi.spyOn(console, "warn"), vi.spyOn(console, "error"), vi.spyOn(console, "debug")];
		const env = { GH_TOKEN: "secret123", ANTHROPIC_API_KEY: "anthropic-test-secret", OPENAI_API_KEY: "openai-test-secret" };
		const success = prepareTriage(env);
		child.stdout.write(JSON.stringify({ ...payload, env })); child.emit("close", 0);
		expect(await success).toEqual({ ok: true, prompt: payload.prompt, dir: payload.dir });
		expect(spawn).toHaveBeenCalledWith("/bundled resources/daemon/ao", ["triage", "--no-launch", "--json"], { env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true });
		for (const log of logs) expect(log).not.toHaveBeenCalled();
		const failure = prepareTriage(env);
		child.stderr.write(`failed with ${Object.values(env).join(" ")}`);
		child.emit("error", new Error("GH_TOKEN=secret123"));
		expect(await failure).toEqual({ ok: false, error: "report.prepareFailed" });
		expect(console.error).toHaveBeenCalledOnce();
		for (const log of logs) {
			const text = JSON.stringify(log.mock.calls);
			for (const value of [...Object.values(env), payload.prompt]) expect(text).not.toContain(value);
		}
	});
	it("handles a synchronous spawn failure", async () => {
		vi.mocked(spawn).mockImplementation(() => { throw new Error("spawn failed"); });
		expect((await prepare()).ok).toBe(false);
		expect(console.error).toHaveBeenCalledExactlyOnceWith("Report a problem prepare failed: could not start command; stderr: (empty)");
	});
	it("caps stdout and kills the child", async () => {
		const result = prepare(); child.stdout.write("x".repeat(1024 * 1024 + 1));
		expect(await result).toEqual({ ok: false, error: "report.tooLarge" });
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
		child.stderr.write("collection stalled");
		await vi.advanceTimersByTimeAsync(19_999);
		expect(child.kill).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1);
		expect(await result).toEqual({ ok: false, error: "report.timedOut" });
		expect(child.kill).toHaveBeenCalledWith("SIGKILL");
		child.emit("close", 0);
		expect(console.error).toHaveBeenCalledExactlyOnceWith("Report a problem prepare failed: report.timedOut; stderr: collection stalled");
	});
});

it("uses the shared Windows dev-daemon manifest path", async () => {
 vi.stubGlobal("process", { ...process, platform: "win32" });
 vi.spyOn(app, "isPackaged", "get").mockReturnValue(false);
 const result = prepareTriage({ AO_DEV_DAEMON_BINARY: "C:\\app\\daemon\\dev-stamp\\ao.exe", PATH: "C:\\tools" });
 child.stdout.write(JSON.stringify(payload)); child.emit("close", 0);
 expect(await result).toMatchObject({ ok: true });
 expect(spawn).toHaveBeenCalledWith("C:\\app\\daemon\\dev-stamp\\ao.exe", expect.any(Array), expect.any(Object));
});
