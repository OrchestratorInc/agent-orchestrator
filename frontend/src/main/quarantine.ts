import { execFile } from "node:child_process";

export type RunFile = (file: string, args: readonly string[]) => Promise<void>;

const runFile: RunFile = (file, args) => new Promise((resolve, reject) => {
	execFile(file, [...args], (error) => (error ? reject(error) : resolve()));
});

export function quarantineValue(agent: string, now = Date.now()): string {
	return `0083;${Math.floor(now / 1000).toString(16)};${agent.replace(/;/g, "")};`;
}

export async function markDownloaded(
	filePath: string,
	agent: string,
	options: { platform?: NodeJS.Platform; run?: RunFile; now?: number } = {},
): Promise<void> {
	if ((options.platform ?? process.platform) !== "darwin") return;
	await (options.run ?? runFile)("/usr/bin/xattr", ["-w", "com.apple.quarantine", quarantineValue(agent, options.now), filePath]);
}
