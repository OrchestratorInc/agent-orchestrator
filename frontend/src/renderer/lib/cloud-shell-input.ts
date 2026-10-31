// Input a Cloud shell types once it first connects: a command cue's command,
// run in the new shell the cue opened. Keyed by the shell's handle and taken
// by the terminal pane when it builds that shell's connection.
const queuedInput = new Map<string, string>();

export function queueCloudShellInput(handleId: string, input: string): void {
	queuedInput.set(handleId, (queuedInput.get(handleId) ?? "") + input);
}

export function takeCloudShellInput(handleId: string): string | undefined {
	const input = queuedInput.get(handleId);
	queuedInput.delete(handleId);
	return input;
}
