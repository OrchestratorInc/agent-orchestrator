// State behind the full-screen update screen (UpdateScreen.tsx), kept outside
// React so any caller — UpdatesManager, the Settings row — can raise it without
// threading a context through. No React Native imports, so vitest covers the
// sequencing.
//
// Two phases: `ready` offers a downloaded update and waits for the user to tap
// Restart (or Not now); `restarting` holds the mascot on screen while the app
// reloads into the update. Nothing here restarts the app without a tap.

export type UpdateScreenPhase = "ready" | "restarting";

export type UpdateScreenState = { visible: false } | { visible: true; phase: UpdateScreenPhase };

const HIDDEN: UpdateScreenState = { visible: false };

let state: UpdateScreenState = HIDDEN;
const listeners = new Set<() => void>();

function set(next: UpdateScreenState): void {
	state = next;
	for (const listener of listeners) listener();
}

export function subscribeUpdateScreen(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

export function getUpdateScreen(): UpdateScreenState {
	return state;
}

/**
 * How long the restarting screen stays up before the reload. The reload itself
 * cuts to a blank root and back, so without a hold the change of state would be
 * a one-frame flash.
 */
export const RESTART_HOLD_MS = 900;

/** Offer a downloaded update. Never replaces a restart already under way. */
export function showUpdateReady(): void {
	if (state.visible && state.phase === "restarting") return;
	set({ visible: true, phase: "ready" });
}

/** Not now: back to the app. A restart already under way is not interrupted. */
export function dismissUpdateScreen(): void {
	if (state.visible && state.phase === "restarting") return;
	set(HIDDEN);
}

export type ReloadApi = { reloadAsync(): Promise<void> };

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/**
 * The user asked to restart: show the restarting screen, hold it, then reload
 * into the downloaded update. A reload that fails takes the screen down again
 * so the app stays usable, and rethrows for the caller to log.
 */
export async function applyUpdate(api: ReloadApi, hold = RESTART_HOLD_MS, sleep = wait): Promise<void> {
	set({ visible: true, phase: "restarting" });
	await sleep(hold);
	try {
		await api.reloadAsync();
	} catch (error) {
		set(HIDDEN);
		throw error;
	}
}
