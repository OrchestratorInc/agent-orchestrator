import path from "node:path";
import { lstatSync, readlinkSync } from "node:fs";
import { canonicalPathInside, sameCanonicalPath } from "../shared/path-identity";

/** Validate before the lab creates any files, including existing symlink aliases. */
export function isolatedLiveGridRoot(root: string, home: string): string {
	const resolved = path.resolve(root);
	const aoRoot = path.join(home, ".ao");
	if (
		sameCanonicalPath(resolved, aoRoot) ||
		["data", "electron", "br", "dev"].some((name) => canonicalPathInside(resolved, path.join(aoRoot, name)))
	) {
		throw new Error("The Live Grid lab requires its own state directory.");
	}
	for (const name of ["data", "electron", "br", "running.json", "remotes.json", "cloud-auth.bin", "app-state.json", "daemon.log", "update-settings.json"]) {
		const child = path.join(resolved, name);
		let target = child;
		try {
			// realpath alone cannot resolve a dangling credential symlink. Check
			// its destination before any write can create it in the real profile.
			if (lstatSync(child).isSymbolicLink()) target = path.resolve(resolved, readlinkSync(child));
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}
		if (!canonicalPathInside(target, resolved)) {
			throw new Error("The Live Grid lab state must stay inside its isolated directory.");
		}
	}
	return resolved;
}
