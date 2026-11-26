// macOS marks a file a browser downloads with com.apple.quarantine, so
// Gatekeeper checks it before it runs and Finder says where it came from.
// Electron's own downloads get it from Chromium; a file this app writes from
// bytes it fetched (a Cloud attachment, a chat image) is marked here instead.

import { execFile } from "node:child_process";

/** Runs a program with arguments and no shell, as execFile does. */
export type RunFile = (file: string, args: readonly string[]) => Promise<void>;

const runFile: RunFile = (file, args) => new Promise((resolve, reject) => {
	execFile(file, [...args], (error) => (error ? reject(error) : resolve()));
});

/**
 * The value a browser download carries: flags 0083 (downloaded, as Chromium
 * writes it), the time in hex seconds, and the app that saved it. A ";" would
 * end the agent's field, so none is kept.
 */
export function quarantineValue(agent: string, now = Date.now()): string {
	return `0083;${Math.floor(now / 1000).toString(16)};${agent.replace(/;/g, "")};`;
}

/** Mark a file this app saved as downloaded; nothing on other platforms. */
export async function markDownloaded(
	filePath: string,
	agent: string,
	options: { platform?: NodeJS.Platform; run?: RunFile; now?: number } = {},
): Promise<void> {
	if ((options.platform ?? process.platform) !== "darwin") return;
	await (options.run ?? runFile)("/usr/bin/xattr", ["-w", "com.apple.quarantine", quarantineValue(agent, options.now), filePath]);
}
