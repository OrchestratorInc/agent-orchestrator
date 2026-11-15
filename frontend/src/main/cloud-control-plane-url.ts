export type ControlPlaneDaemon = { baseUrl: string; key: string };

export type ControlPlaneUrlResolver = () => Promise<string | null>;

export function createControlPlaneUrlResolver(
	daemon: () => ControlPlaneDaemon | null,
	fetchImpl: typeof fetch = (input, init) => fetch(input, init),
): ControlPlaneUrlResolver {
	let cached: { key: string; url: Promise<string | null> } | undefined;
	return () => {
		const current = daemon();
		if (!current) return Promise.resolve(null);
		if (cached?.key !== current.key) {
			const entry = {
				key: current.key,
				url: (async () => {
					const response = await fetchImpl(`${current.baseUrl.replace(/\/+$/, "")}/api/v1/settings`, { signal: AbortSignal.timeout(5_000) });
					if (!response.ok) throw new Error(`settings answered ${response.status}`);
					const body = (await response.json()) as { cloudControlPlaneUrl?: unknown } | null;
					const url = body?.cloudControlPlaneUrl;
					return typeof url === "string" && url.trim() !== "" ? url.trim() : null;
				})(),
			};
			cached = entry;
			entry.url.catch(() => {
				if (cached === entry) cached = undefined;
			});
		}
		return cached.url.catch(() => null);
	};
}

export function sameControlPlaneUrl(a: string, b: string): boolean {
	try {
		const left = new URL(a);
		const right = new URL(b);
		return left.origin === right.origin && left.pathname.replace(/\/+$/, "") === right.pathname.replace(/\/+$/, "");
	} catch {
		return false;
	}
}
