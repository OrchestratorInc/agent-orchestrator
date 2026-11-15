// Reads a session's project "always Temporary" browser setting from the local
// daemon. Any failure answers false, so the browser falls back to the default
// profile instead of blocking.

const LOOKUP_TIMEOUT_MS = 3_000;

export type ProjectBrowserTemporaryLookupOptions = {
	daemonPort: () => number | undefined;
	fetch?: typeof fetch;
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
	return async (sessionId) => {
		const port = options.daemonPort();
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
