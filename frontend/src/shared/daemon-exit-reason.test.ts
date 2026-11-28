import { describe, expect, it } from "vitest";
import { daemonExitReason } from "./daemon-exit-reason";

describe("daemonExitReason", () => {
	it("reads the fatal error on the last line", () => {
		const logs = '{"level":"INFO","msg":"starting"}\n';
		expect(daemonExitReason(`${logs}daemon already running (pid 412, port 3001); refusing to start\n`, 1)).toBe("already_running");
		expect(daemonExitReason(`${logs}open store: migrate: database disk image is malformed`, 1)).toBe("store_open_failed");
		expect(daemonExitReason(`${logs}inspect run-file: read C:\\Users\\me\\.ao\\running.json: Access is denied.`, 1)).toBe("run_file_unreadable");
		expect(daemonExitReason(`${logs}daemon working directory: create /Users/me/.ao: permission denied`, 1)).toBe("working_dir_failed");
		expect(daemonExitReason(`${logs}listen tcp 127.0.0.1:3001: bind: address already in use`, 1)).toBe("port_in_use");
		expect(daemonExitReason(`${logs}write /Users/me/.ao/ao.db-wal: no space left on device`, 1)).toBe("disk_full");
		expect(daemonExitReason(`${logs}open /Users/me/.ao/token: operation not permitted`, 1)).toBe("permission_denied");
		expect(daemonExitReason(`${logs}listen tcp 127.0.0.1:80: bind: permission denied`, 1)).toBe("permission_denied");
	});

	it("knows the data-dir lock by its exit code or its message", () => {
		expect(daemonExitReason("", 75)).toBe("data_dir_locked");
		expect(daemonExitReason("data dir is in use by another Orchestrator process; waited 6m22s for it to exit, refusing to start", 1)).toBe("data_dir_locked");
	});

	it("knows a panic by its header, even with a stack after it", () => {
		expect(daemonExitReason("panic: runtime error: index out of range\n\ngoroutine 1 [running]:\nmain.main()\n\t/src/main.go:12", 2)).toBe("panic");
		expect(daemonExitReason("fatal error: concurrent map writes\n\ngoroutine 9 [running]:", 2)).toBe("panic");
	});

	it("ignores an earlier warning that is not why it stopped", () => {
		const output = 'WARN permission denied reading /Users/me/.config/gh\n{"level":"INFO","msg":"serving"}\n{"level":"INFO","msg":"shutting down"}\n{"level":"INFO","msg":"stopped"}\nsomething unexpected happened';
		expect(daemonExitReason(output, 1)).toBe("unknown");
	});

	it("reads only the last line, not a warning just before it", () => {
		expect(daemonExitReason("WARN permission denied reading configuration\nsomething unexpected happened", 1)).toBe("unknown");
	});

	it("is unknown with no output", () => {
		expect(daemonExitReason(undefined, 1)).toBe("unknown");
		expect(daemonExitReason("", null)).toBe("unknown");
	});
});
