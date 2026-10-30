// This is a desktop preference, shared by sessions in one project. Storage
// failures must never turn a successfully dispatched cue into a failed run.
const storagePrefix = "ao.cues.lastRun.";
const remembered = new Map<string, string>();

export function readLastRunCue(projectId: string): string | null {
	try {
		return remembered.get(projectId) ?? window.localStorage.getItem(storagePrefix + projectId) ?? null;
	} catch {
		return remembered.get(projectId) ?? null;
	}
}

export function rememberLastRunCue(projectId: string, cueId: string): void {
	remembered.set(projectId, cueId);
	try {
		window.localStorage.setItem(storagePrefix + projectId, cueId);
	} catch {
		// Keep the preference in memory when browser storage is unavailable.
	}
}
