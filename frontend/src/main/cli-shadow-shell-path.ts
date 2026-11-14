export function createCliShadowShellPath(input: {
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
