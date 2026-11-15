function joinPath(...segments: string[]): string {
	return segments.map((segment) => segment.replace(/[/\\]+$/, "")).join("/");
}

export function bundledTmuxBinaryPath(
	isPackaged: boolean,
	resourcesPath: string,
	platform: NodeJS.Platform,
): string | null {
	if (!isPackaged || (platform !== "darwin" && platform !== "linux")) return null;
	return joinPath(resourcesPath, "tmux", "bin", "tmux");
}

export function stableBundledTmuxBinaryPath(
	isPackaged: boolean,
	aoDataDir: string,
	appVersion: string,
	platform: NodeJS.Platform,
	arch: string,
): string | null {
	if (!isPackaged || (platform !== "darwin" && platform !== "linux")) return null;
	const identity = `${appVersion}-${platform}-${arch}`.replace(/[^a-zA-Z0-9._-]/g, "_");
	return joinPath(aoDataDir, "runtime", "tmux", identity, "tmux");
}
