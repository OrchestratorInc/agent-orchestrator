import type { components } from "../api/schema";

/** Observe local system effects independently of renderer/window lifetime. */
export function createAwakeTrayMonitor(options: {
	baseURL: () => string | null;
	request: (url: string, init: RequestInit) => Promise<Response>;
	setState: (active: boolean | null) => void;
}) {
	let disposed = false;
	let inFlight = false;
	const poll = async () => {
		if (disposed || inFlight) return;
		const base = options.baseURL();
		if (!base) { options.setState(null); return; }
		inFlight = true;
		try {
			const read = async (route: string) => {
				const response = await options.request(`${base}/api/v1/${route}`, { signal: AbortSignal.timeout(3000) });
				if (!response.ok) throw new Error("Stay Awake status unavailable");
				return response.json();
			};
			const states = await Promise.allSettled([
				read("awake").then((state: components["schemas"]["AwakeState"]) => state.active),
				read("mobile/status").then((state: components["schemas"]["MobileStatusResponse"]) => state.keepAwake?.active),
			]);
			// A failed probe is not evidence that an existing wake hold ended.
			const active = states.some((state) => state.status === "fulfilled" && state.value === true)
				? true
				: states.every((state) => state.status === "fulfilled" && state.value === false) ? false : null;
			if (!disposed && base === options.baseURL()) options.setState(active);
		} finally {
			inFlight = false;
		}
	};
	void poll();
	const timer = setInterval(() => void poll(), 5000);
	timer.unref?.();
	return {
		dispose() {
			disposed = true;
			clearInterval(timer);
		},
	};
}
