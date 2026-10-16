// When the shell renderer dies (crash, OOM, kill), Electron keeps the window but
// nothing paints into its transparent shell view, so the user sees a blank
// window until they close it and reopen it from the tray. Rebuilding the window
// is the path that manual recovery already proves works; this module decides
// when to take it and stops a renderer that dies on every load from looping.

export type ShellGoneDetails = { reason: string; exitCode: number };

export type ShellWindowSnapshot = {
	/** Normal (unmaximized) bounds, so a maximized or full-screen window restores correctly. */
	bounds: { x: number; y: number; width: number; height: number };
	fullScreen: boolean;
	maximized: boolean;
	minimized: boolean;
	visible: boolean;
	focused: boolean;
};

/** "crash" is automatic; every other trigger is a user action that brings the window forward. */
export type ShellRecoveryTrigger = "crash" | "dialog" | "focus" | "activate";

export const SHELL_RECOVERY_MAX_ATTEMPTS = 3;
export const SHELL_RECOVERY_WINDOW_MS = 10 * 60_000;

export type CrashLoopGuard = {
	/** Records an automatic attempt when the window still allows one. */
	tryAcquire(now: number): boolean;
	/** Records a user-initiated attempt, which is never refused. */
	record(now: number): void;
};

export function createCrashLoopGuard(
	maxAttempts = SHELL_RECOVERY_MAX_ATTEMPTS,
	windowMs = SHELL_RECOVERY_WINDOW_MS,
): CrashLoopGuard {
	const attempts: number[] = [];
	const prune = (now: number) => {
		while (attempts.length > 0 && now - attempts[0] >= windowMs) attempts.shift();
	};
	return {
		tryAcquire(now) {
			prune(now);
			if (attempts.length >= maxAttempts) return false;
			attempts.push(now);
			return true;
		},
		record(now) {
			prune(now);
			attempts.push(now);
		},
	};
}

/**
 * A clean exit is a window or app closing on purpose. Every other reason
 * (crashed, oom, killed, abnormal-exit, launch-failed, ...) leaves a dead window.
 */
export function shouldRecoverShell(details: ShellGoneDetails, state: { quitting: boolean; closing: boolean }): boolean {
	if (details.reason === "clean-exit") return false;
	return !state.quitting && !state.closing;
}

export type ShellRecoveryDeps = {
	now(): number;
	log(message: string): void;
	isQuitting(): boolean;
	isClosing(): boolean;
	/** Snapshot of the window being replaced; null when there is no live window. */
	snapshot(): ShellWindowSnapshot | null;
	/** Destroy the current window and build a fresh one restored to the snapshot. */
	rebuild(snapshot: ShellWindowSnapshot | null, trigger: ShellRecoveryTrigger): Promise<void>;
	/** Shown once automatic recovery gives up. Resolves true when the user asks to reload. */
	confirmReload(): Promise<boolean>;
};

export type ShellRecovery = {
	/** True from the moment a rebuild starts until the replacement window exists. */
	isRecovering(): boolean;
	handleRenderProcessGone(details: ShellGoneDetails): void;
	/** User-initiated rebuild (tray, notification, Dock). Bypasses the crash-loop guard. */
	recoverNow(trigger: Exclude<ShellRecoveryTrigger, "crash">): Promise<void>;
};

export function createShellRecovery(deps: ShellRecoveryDeps, guard: CrashLoopGuard = createCrashLoopGuard()): ShellRecovery {
	let inFlight: Promise<void> | null = null;
	let promptOpen = false;

	const run = (trigger: ShellRecoveryTrigger): Promise<void> => {
		if (inFlight) return inFlight;
		const snapshot = deps.snapshot();
		deps.log(`AO: rebuilding the main window after its renderer died (trigger=${trigger})`);
		let settle!: () => void;
		inFlight = new Promise<void>((resolve) => { settle = resolve; });
		const current = inFlight;
		// inFlight is set before rebuild runs: destroying the old window emits
		// "closed", and that handler must already see a recovery in progress.
		void Promise.resolve()
			.then(() => deps.rebuild(snapshot, trigger))
			.catch((error: unknown) => {
				deps.log(`AO: main window rebuild failed: ${error instanceof Error ? error.message : String(error)}`);
			})
			.finally(() => {
				if (inFlight === current) inFlight = null;
				settle();
			});
		return current;
	};

	return {
		isRecovering: () => inFlight !== null,
		handleRenderProcessGone(details) {
			deps.log(`AO: shell renderer gone reason=${details.reason} exitCode=${details.exitCode}`);
			if (!shouldRecoverShell(details, { quitting: deps.isQuitting(), closing: deps.isClosing() })) return;
			if (guard.tryAcquire(deps.now())) {
				void run("crash");
				return;
			}
			deps.log(
				`AO: shell renderer died ${SHELL_RECOVERY_MAX_ATTEMPTS} times within ${SHELL_RECOVERY_WINDOW_MS / 60_000} minutes; automatic recovery paused`,
			);
			if (promptOpen) return;
			promptOpen = true;
			void deps.confirmReload().then(
				(reload) => {
					promptOpen = false;
					if (!reload || deps.isQuitting()) return;
					guard.record(deps.now());
					void run("dialog");
				},
				() => {
					promptOpen = false;
				},
			);
		},
		recoverNow(trigger) {
			guard.record(deps.now());
			return run(trigger);
		},
	};
}
