// Reads a session's project "always Temporary" browser setting from the local
// daemon. Any failure answers false, so the browser falls back to the default
// profile instead of blocking.

const LOOKUP_TIMEOUT_MS = 3_000;
// Right after launch the daemon may not be ready yet. Wait for it briefly
// rather than opening an always-Temporary project's session in the
// persistent profile.
const DAEMON_READY_TIMEOUT_MS = 5_000;
const DAEMON_READY_POLL_MS = 100;

export type ProjectBrowserTemporaryLookupOptions = {
	daemonPort: () => number | undefined;
	fetch?: typeof fetch;
	readyTimeoutMs?: number;
	readyPollMs?: number;
};

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function createProjectBrowserTemporaryLookup(
	options: ProjectBrowserTemporaryLookupOptions,
): (sessionId: string) => Promise<boolean> {
	const fetchImpl = options.fetch ?? fetch;
	const getJSON = async (port: number, path: string): Promise<unknown> => {
		const response = await fetchImpl(`http://127.0.0.1:${port}${path}`, {
			redirect: "error",
			signal: AbortSignal.timeout(LOOKUP_TIMEOUT_MS),
		});
		if (!response.ok) return undefined;
		return response.json();
	};
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
	return async (sessionId) => {
		const port = await waitForPort();
		if (!port) return false;
		try {
			const sessionBody = await getJSON(port, `/api/v1/sessions/${encodeURIComponent(sessionId)}`);
			const session = isRecord(sessionBody) ? sessionBody.session : undefined;
			const projectId = isRecord(session) ? session.projectId : undefined;
			if (typeof projectId !== "string" || !projectId) return false;
			const projectBody = await getJSON(port, `/api/v1/projects/${encodeURIComponent(projectId)}`);
			const project = isRecord(projectBody) ? projectBody.project : undefined;
			const config = isRecord(project) ? project.config : undefined;
			return isRecord(config) && config.browserTemporary === true;
		} catch {
			return false;
		}
	};
}
