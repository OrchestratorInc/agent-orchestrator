const queuedInput = new Map<string, string>();

export function queueCloudShellInput(handleId: string, input: string): void {
	queuedInput.set(handleId, (queuedInput.get(handleId) ?? "") + input);
}

export function takeCloudShellInput(handleId: string): string | undefined {
	const input = queuedInput.get(handleId);
	queuedInput.delete(handleId);
	return input;
}
