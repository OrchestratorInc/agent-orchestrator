import { describe, expect, it, vi } from "vitest";
import {
	SHELL_RECOVERY_WINDOW_MS,
	createCrashLoopGuard,
	createShellRecovery,
	shouldRecoverShell,
	type ShellRecoveryDeps,
	type ShellWindowSnapshot,
} from "./shell-recovery";

const crashed = { reason: "crashed", exitCode: 5 };
const snapshot: ShellWindowSnapshot = {
	bounds: { x: 10, y: 20, width: 1320, height: 860 },
	fullScreen: false,
	maximized: false,
	minimized: false,
	visible: true,
	focused: false,
};

function setup(overrides: Partial<ShellRecoveryDeps> = {}) {
	let now = 0;
	const deps: ShellRecoveryDeps = {
		now: () => now,
		log: vi.fn(),
		isQuitting: () => false,
		isClosing: () => false,
		snapshot: vi.fn(() => snapshot),
		rebuild: vi.fn(async () => undefined),
		confirmReload: vi.fn(async () => false),
		...overrides,
	};
	const recovery = createShellRecovery(deps);
	return {
		deps,
		recovery,
		advance: (ms: number) => {
			now += ms;
		},
	};
}

async function settle() {
	for (let i = 0; i < 5; i++) await Promise.resolve();
}

describe("shouldRecoverShell", () => {
	it.each(["crashed", "oom", "killed", "abnormal-exit", "launch-failed"])("recovers after %s", (reason) => {
		expect(shouldRecoverShell({ reason, exitCode: 1 }, { quitting: false, closing: false })).toBe(true);
	});

	it("leaves a clean exit alone", () => {
		expect(shouldRecoverShell({ reason: "clean-exit", exitCode: 0 }, { quitting: false, closing: false })).toBe(false);
	});

	it("does not rebuild a window that is quitting or closing", () => {
		expect(shouldRecoverShell(crashed, { quitting: true, closing: false })).toBe(false);
		expect(shouldRecoverShell(crashed, { quitting: false, closing: true })).toBe(false);
	});
});

describe("createCrashLoopGuard", () => {
	it("allows three automatic attempts per ten minutes, then frees slots as they age out", () => {
		const guard = createCrashLoopGuard();
		expect(guard.tryAcquire(0)).toBe(true);
		expect(guard.tryAcquire(1_000)).toBe(true);
		expect(guard.tryAcquire(2_000)).toBe(true);
		expect(guard.tryAcquire(3_000)).toBe(false);
		expect(guard.tryAcquire(SHELL_RECOVERY_WINDOW_MS - 1)).toBe(false);
		expect(guard.tryAcquire(SHELL_RECOVERY_WINDOW_MS)).toBe(true);
	});

	it("counts user-initiated attempts toward the window", () => {
		const guard = createCrashLoopGuard();
		guard.record(0);
		guard.record(0);
		guard.record(0);
		expect(guard.tryAcquire(1)).toBe(false);
	});
});

describe("createShellRecovery", () => {
	it("rebuilds the window from a snapshot after a crash and logs the reason", async () => {
		const { deps, recovery } = setup();
		recovery.handleRenderProcessGone({ reason: "oom", exitCode: 133 });
		expect(recovery.isRecovering()).toBe(true);
		await settle();
		expect(deps.rebuild).toHaveBeenCalledWith(snapshot, "crash");
		expect(deps.log).toHaveBeenCalledWith("AO: shell renderer gone reason=oom exitCode=133");
		expect(recovery.isRecovering()).toBe(false);
	});

	it("marks recovery in progress before the old window is destroyed", async () => {
		const seen: boolean[] = [];
		let recovery!: ReturnType<typeof createShellRecovery>;
		({ recovery } = setup({
			rebuild: vi.fn(async () => {
				seen.push(recovery.isRecovering());
			}),
		}));
		recovery.handleRenderProcessGone(crashed);
		await settle();
		expect(seen).toEqual([true]);
	});

	it("ignores a clean exit and a crash during quit", async () => {
		const quitting = setup({ isQuitting: () => true });
		quitting.recovery.handleRenderProcessGone(crashed);
		const clean = setup();
		clean.recovery.handleRenderProcessGone({ reason: "clean-exit", exitCode: 0 });
		await settle();
		expect(quitting.deps.rebuild).not.toHaveBeenCalled();
		expect(clean.deps.rebuild).not.toHaveBeenCalled();
	});

	it("joins a rebuild already in flight instead of starting a second one", async () => {
		let finish!: () => void;
		const { deps, recovery } = setup({
			rebuild: vi.fn(() => new Promise<void>((resolve) => { finish = resolve; })),
		});
		recovery.handleRenderProcessGone(crashed);
		const joined = recovery.recoverNow("focus");
		await settle();
		expect(deps.rebuild).toHaveBeenCalledTimes(1);
		finish();
		await joined;
		expect(recovery.isRecovering()).toBe(false);
	});

	it("stops looping after three crashes in ten minutes and asks the user once", async () => {
		let answer!: (reload: boolean) => void;
		const { deps, recovery, advance } = setup({
			confirmReload: vi.fn(() => new Promise<boolean>((resolve) => { answer = resolve; })),
		});
		for (let i = 0; i < 3; i++) {
			recovery.handleRenderProcessGone(crashed);
			await settle();
			advance(60_000);
		}
		expect(deps.rebuild).toHaveBeenCalledTimes(3);

		recovery.handleRenderProcessGone(crashed);
		recovery.handleRenderProcessGone(crashed);
		await settle();
		expect(deps.rebuild).toHaveBeenCalledTimes(3);
		expect(deps.confirmReload).toHaveBeenCalledTimes(1);

		answer(true);
		await settle();
		expect(deps.rebuild).toHaveBeenCalledTimes(4);
		expect(deps.rebuild).toHaveBeenLastCalledWith(snapshot, "dialog");
	});

	it("does nothing when the user dismisses the crash-loop prompt", async () => {
		const { deps, recovery } = setup({ confirmReload: vi.fn(async () => false) });
		for (let i = 0; i < 4; i++) {
			recovery.handleRenderProcessGone(crashed);
			await settle();
		}
		expect(deps.confirmReload).toHaveBeenCalledTimes(1);
		expect(deps.rebuild).toHaveBeenCalledTimes(3);
	});

	it("lets the tray rebuild even after automatic recovery gave up", async () => {
		const { deps, recovery } = setup();
		for (let i = 0; i < 4; i++) {
			recovery.handleRenderProcessGone(crashed);
			await settle();
		}
		await recovery.recoverNow("focus");
		expect(deps.rebuild).toHaveBeenLastCalledWith(snapshot, "focus");
		expect(deps.rebuild).toHaveBeenCalledTimes(4);
	});

	it("logs a failed rebuild and clears the in-flight state", async () => {
		const { deps, recovery } = setup({ rebuild: vi.fn(async () => { throw new Error("boom"); }) });
		await recovery.recoverNow("activate");
		expect(deps.log).toHaveBeenCalledWith("AO: main window rebuild failed: boom");
		expect(recovery.isRecovering()).toBe(false);
	});
});
