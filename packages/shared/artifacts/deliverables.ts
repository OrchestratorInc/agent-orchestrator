/**
 * Which of a session's artifact files the artifact lists show. Both servers
 * return every file with a `registered` fact; this one rule decides, so the
 * desktop and the phone cannot disagree and local and cloud sessions behave the
 * same. The full list still backs links, chat images and the delete count.
 */

/**
 * Directories that are one document on macOS. Keep this in step with
 * bundleSuffixes in backend/internal/sessionartifacts.
 */
export const BUNDLE_SUFFIXES = [".icon", ".app", ".bundle", ".xcassets"] as const;

export type ArtifactFacts = {
	path: string;
	name: string;
	kind: string;
	size: number;
	updatedAt: string;
	registered?: boolean;
};

/** Set on an entry that stands for a whole bundle directory. */
export type ArtifactBundle = { path: string; fileCount: number };

export type Deliverable<T extends ArtifactFacts> = T & { bundle?: ArtifactBundle };

const IMAGE = /\.(apng|avif|bmp|gif|heic|heif|jpe?g|png|svg|webp)$/i;

/**
 * The files to list, in the order given. Files in a bundle directory collapse
 * into one entry. Then: the registered entries if there are any, or else those
 * at the root of the artifact directory, or else all of them. A session from
 * before registration, or whose agent never registered, takes the fallback.
 */
export function deliverableArtifacts<T extends ArtifactFacts>(files: readonly T[]): Deliverable<T>[] {
	const entries = collapseBundles(files);
	const registered = entries.filter((entry) => entry.file.registered);
	if (registered.length > 0) return registered.map((entry) => entry.file);
	const root = entries.filter((entry) => !entry.location.includes("/"));
	return (root.length > 0 ? root : entries).map((entry) => entry.file);
}

/** The bundle directory path contains, if any: the outermost one. */
export function bundleDirectory(path: string): string | undefined {
	const parts = path.split("/");
	for (let index = 0; index < parts.length - 1; index += 1) {
		const name = parts[index].toLowerCase();
		if (BUNDLE_SUFFIXES.some((suffix) => name.endsWith(suffix))) return parts.slice(0, index + 1).join("/");
	}
	return undefined;
}

function collapseBundles<T extends ArtifactFacts>(files: readonly T[]): { location: string; file: Deliverable<T> }[] {
	const bundles = new Map<string, T[]>();
	for (const file of files) {
		const directory = bundleDirectory(file.path);
		if (directory === undefined) continue;
		const members = bundles.get(directory);
		if (members) members.push(file);
		else bundles.set(directory, [file]);
	}
	const seen = new Set<string>();
	const entries: { location: string; file: Deliverable<T> }[] = [];
	for (const file of files) {
		const directory = bundleDirectory(file.path);
		if (directory === undefined) {
			entries.push({ location: file.path, file });
		} else if (!seen.has(directory)) {
			seen.add(directory);
			entries.push({ location: directory, file: bundleEntry(directory, bundles.get(directory) ?? [file]) });
		}
	}
	return entries;
}

/**
 * A bundle as one entry. It keeps a real file's path, the one it is most useful
 * to open, so the viewer and every route that takes a path still work.
 */
function bundleEntry<T extends ArtifactFacts>(directory: string, members: readonly T[]): Deliverable<T> {
	const main = members.find((file) => file.kind === "html")
		?? members.find((file) => file.kind === "markdown")
		?? members.find((file) => IMAGE.test(file.path))
		?? members[0];
	return {
		...main,
		name: directory.split("/").pop() ?? directory,
		size: members.reduce((total, file) => total + file.size, 0),
		updatedAt: members.reduce((newest, file) => (timeOf(file.updatedAt) > timeOf(newest) ? file.updatedAt : newest), main.updatedAt),
		registered: members.some((file) => file.registered),
		bundle: { path: directory, fileCount: members.length },
	};
}

function timeOf(value: string): number {
	return Date.parse(value) || 0;
}
