type ChildStream = { readableEnded?: boolean; once(event: "close", listener: () => void): unknown } | null | undefined;

/**
 * Waits for a child's piped stdout and stderr to close, for at most
 * `timeoutMs`: a grandchild can hold a pipe open. Node may report a child's
 * exit before its pipes drain, so the last lines it wrote can arrive after
 * `exit`. Returns undefined when no pipe is still open, so a caller can carry
 * on in the same tick.
 */
export function childStdioDrained(child: { stdout?: ChildStream; stderr?: ChildStream }, timeoutMs: number): Promise<void> | undefined {
	const open = [child.stdout, child.stderr].filter((stream): stream is NonNullable<ChildStream> => stream?.readableEnded === false);
	if (open.length === 0) return undefined;
	return new Promise((resolve) => {
		let remaining = open.length;
		const timer = setTimeout(resolve, timeoutMs);
		for (const stream of open) {
			stream.once("close", () => {
				remaining -= 1;
				if (remaining > 0) return;
				clearTimeout(timer);
				resolve();
			});
		}
	});
}
