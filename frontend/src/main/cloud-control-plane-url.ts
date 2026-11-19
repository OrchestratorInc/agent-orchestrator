// The AO Cloud control plane this app talks to. The daemon owns it (a baked
// default, overridden only by AO_CLOUD_CONTROL_PLANE_URL in its environment)
// and reports it in GET /api/v1/settings as cloudControlPlaneUrl, which is
// where the renderer reads it too. The main process sends the AO Cloud token
// only to this URL, never to a base URL the renderer names.

/** The daemon the resolver asks: its loopback base URL and a key for this attachment. */
export type ControlPlaneDaemon = { baseUrl: string; key: string };

export type ControlPlaneUrlResolver = () => Promise<string | null>;

/**
 * Resolve the configured control-plane URL from the attached daemon, once per
 * attachment (a new daemon, or the same port with a new pid, asks again). A
 * failed read is not kept, so the next request asks again; null means no
 * daemon is attached or it names no control plane.
 */
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

/** Whether two control-plane base URLs name the same server and path. */
export function sameControlPlaneUrl(a: string, b: string): boolean {
	try {
		const left = new URL(a);
		const right = new URL(b);
		return left.origin === right.origin && left.pathname.replace(/\/+$/, "") === right.pathname.replace(/\/+$/, "");
	} catch {
		return false;
	}
}
