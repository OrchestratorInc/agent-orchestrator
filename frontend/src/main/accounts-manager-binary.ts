import path from "node:path";

type ResolveOptions = {
	explicitPath?: string;
	isPackaged: boolean;
	resourcesPath: string;
	appPath: string;
	platform: NodeJS.Platform;
	readDevManifest?: () => string;
};

export function resolveAccountsManagerBinary(options: ResolveOptions): string {
	const explicit = options.explicitPath?.trim();
	if (explicit) return explicit;

	const binaryName = options.platform === "win32" ? "ao-accounts-manager.exe" : "ao-accounts-manager";
	if (options.isPackaged) {
		return path.join(options.resourcesPath, "accounts-manager", binaryName);
	}

	const buildDirectory = path.resolve(options.appPath, "accounts-manager");
	if (options.platform === "win32" && options.readDevManifest) {
		try {
			const parsed = JSON.parse(options.readDevManifest()) as { path?: unknown };
			if (typeof parsed.path === "string" && parsed.path.trim()) {
				const candidate = path.resolve(parsed.path.trim());
				const relative = path.relative(buildDirectory, candidate);
				if (relative && !relative.startsWith("..") && !path.isAbsolute(relative)) {
					return candidate;
				}
			}
		} catch {
			// Fall through to the fixed non-dev build path.
		}
	}
	return path.join(buildDirectory, binaryName);
}
