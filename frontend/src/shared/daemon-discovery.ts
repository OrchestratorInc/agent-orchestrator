function joinPath(...segments: string[]): string {
	return segments.map((segment) => segment.replace(/[/\\]+$/, "")).join("/");
}

export function parseDaemonListenPort(line: string): number | null {
	if (!line.includes('msg="daemon listening"')) return null;
	const addr = /(?:^|\s)addr=("?)([^"\s]+)\1/.exec(line)?.[2];
	if (!addr) return null;
	return portFromAddr(addr);
}

function portFromAddr(addr: string): number | null {
	const separator = addr.lastIndexOf(":");
	if (separator === -1) return null;
	const port = Number(addr.slice(separator + 1));
	return Number.isInteger(port) && port >= 1 && port <= 65535 ? port : null;
}

export function createListenPortScanner(onPort: (port: number) => void): (chunk: string) => void {
	let pending = "";
	let done = false;
	return (chunk) => {
		if (done) return;
		pending += chunk;
		const lines = pending.split("\n");
		pending = lines.pop() ?? "";
		for (const line of lines) {
			const port = parseDaemonListenPort(line);
			if (port !== null) {
				done = true;
				onPort(port);
				return;
			}
		}
	};
}

export type RunFileInfo = {
	pid: number;
	port: number;
	startedAtMs: number;
	owner?: string;
	appRunId?: string;
	browserRuntimeAddress?: string;
	browserStreamAddress?: string;
};

export function parseRunFile(contents: string): RunFileInfo | null {
	let raw: unknown;
	try {
		raw = JSON.parse(contents);
	} catch {
		return null;
	}
	if (typeof raw !== "object" || raw === null) return null;
	const { pid, port, startedAt, owner, appRunId, browserRuntimeAddress, browserStreamAddress } = raw as {
		pid?: unknown;
		port?: unknown;
		startedAt?: unknown;
		owner?: unknown;
		appRunId?: unknown;
		browserRuntimeAddress?: unknown;
		browserStreamAddress?: unknown;
	};
	if (typeof port !== "number" || !Number.isInteger(port) || port < 1 || port > 65535) return null;
	const startedAtMs = typeof startedAt === "string" ? Date.parse(startedAt) : NaN;
	return {
		pid: typeof pid === "number" && Number.isInteger(pid) ? pid : 0,
		port,
		startedAtMs: Number.isNaN(startedAtMs) ? 0 : startedAtMs,
		owner: typeof owner === "string" ? owner : undefined,
		appRunId: typeof appRunId === "string" ? appRunId : undefined,
		browserRuntimeAddress: typeof browserRuntimeAddress === "string" ? browserRuntimeAddress : undefined,
		browserStreamAddress: typeof browserStreamAddress === "string" ? browserStreamAddress : undefined,
	};
}

export function defaultRunFilePath(
	platform: NodeJS.Platform,
	_env: Record<string, string | undefined>,
	homeDir: string,
): string | null {
	void platform;
	if (!homeDir) return null;
	return joinPath(homeDir, ".ao", "running.json");
}
