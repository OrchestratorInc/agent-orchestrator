// Reads a session's project "always Temporary" browser setting from the local
// daemon. A failed lookup is retried once; if it still fails it answers false,
// with a warning, so the browser falls back to the default profile instead of
// blocking.

const LOOKUP_TIMEOUT_MS = 3_000;
// Right after launch the daemon may not be ready yet. Wait for it briefly
// rather than opening an always-Temporary project's session in the
// persistent profile.
const DAEMON_READY_TIMEOUT_MS = 5_000;
const DAEMON_READY_POLL_MS = 100;
const RETRY_DELAY_MS = 250;

class LookupFailed extends Error {}

export type ProjectBrowserTemporaryLookupOptions = {
	daemonPort: () => number | undefined;
	fetch?: typeof fetch;
	readyTimeoutMs?: number;
	readyPollMs?: number;
	retryDelayMs?: number;
	log?: (message: string) => void;
};

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function createProjectBrowserTemporaryLookup(
	options: ProjectBrowserTemporaryLookupOptions,
): (sessionId: string) => Promise<boolean> {
	const fetchImpl = options.fetch ?? fetch;
	// A 404 is a definite answer (the daemon does not know this session or
	// project); any other failure is worth one retry.
	const getJSON = async (port: number, path: string): Promise<unknown> => {
		let response: Response;
		try {
			response = await fetchImpl(`http://127.0.0.1:${port}${path}`, {
				redirect: "error",
				signal: AbortSignal.timeout(LOOKUP_TIMEOUT_MS),
			});
		} catch (error) {
			throw new LookupFailed((error as Error).message);
		}
		if (response.status === 404) return undefined;
		if (!response.ok) throw new LookupFailed(`HTTP ${response.status}`);
		try {
			return await response.json();
		} catch (error) {
			throw new LookupFailed((error as Error).message);
		}
	};
	const retryDelayMs = options.retryDelayMs ?? RETRY_DELAY_MS;
	const log = options.log ?? ((message: string) => console.warn(message));
	const readyTimeoutMs = options.readyTimeoutMs ?? DAEMON_READY_TIMEOUT_MS;
	const readyPollMs = options.readyPollMs ?? DAEMON_READY_POLL_MS;
	const waitForPort = async (): Promise<number | undefined> => {
		const deadline = Date.now() + readyTimeoutMs;
		for (;;) {
			const port = options.daemonPort();
			if (port || Date.now() >= deadline) return port;
			await new Promise((resolve) => setTimeout(resolve, readyPollMs));
		}
	};
	const read = async (port: number, sessionId: string): Promise<boolean> => {
		const sessionBody = await getJSON(port, `/api/v1/sessions/${encodeURIComponent(sessionId)}`);
		const session = isRecord(sessionBody) ? sessionBody.session : undefined;
		const projectId = isRecord(session) ? session.projectId : undefined;
		if (typeof projectId !== "string" || !projectId) return false;
		const projectBody = await getJSON(port, `/api/v1/projects/${encodeURIComponent(projectId)}`);
		const project = isRecord(projectBody) ? projectBody.project : undefined;
		const config = isRecord(project) ? project.config : undefined;
		return isRecord(config) && config.browserTemporary === true;
	};
	return async (sessionId) => {
		const port = await waitForPort();
		if (!port) {
			log(`AO: daemon not ready; opening session ${sessionId}'s browser without its project browser setting`);
			return false;
		}
		for (let attempt = 1; ; attempt += 1) {
			try {
				return await read(port, sessionId);
			} catch (error) {
				if (attempt >= 2) {
					log(`AO: could not read session ${sessionId}'s project browser setting (${(error as Error).message}); using the default profile`);
					return false;
				}
				await new Promise((resolve) => setTimeout(resolve, retryDelayMs));
			}
		}
	};
}
