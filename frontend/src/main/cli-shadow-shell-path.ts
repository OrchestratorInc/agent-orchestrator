// The startup probe's limit exists because the daemon spawn waits on it, and a
// shell near that limit fails it under launch load. Nothing waits on the
// advisory shadow check, so it can give the shell longer, without touching the
// env the daemon uses. Takes no fallback: a Finder launch's process PATH is not
// the user's shell PATH, so a shell that never answers means "unknown".
export function createCliShadowShellPath(input: {
	// Settles the startup probes, whose answer is preferred when they have one.
	settleStartupProbes: () => Promise<void>;
	startupPath: () => string | undefined;
	probe: () => Promise<string | undefined>;
}): () => Promise<string | undefined> {
	let pending: Promise<string | undefined> | null = null;
	return () => {
		pending ??= (async () => {
			try {
				await input.settleStartupProbes();
				const path = input.startupPath() ?? (await input.probe());
				// Keep only an answer, so opening Settings can try again after a failure.
				if (!path) pending = null;
				return path;
			} catch (error) {
				pending = null;
				throw error;
			}
		})();
		return pending;
	};
}
