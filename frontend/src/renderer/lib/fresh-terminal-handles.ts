const freshTerminalHandles = new Set<string>();

export function markTerminalHandleFresh(handleId: string): void {
	freshTerminalHandles.add(handleId);
}

export function consumeFreshTerminalHandle(handleId: string): boolean {
	return freshTerminalHandles.delete(handleId);
}
