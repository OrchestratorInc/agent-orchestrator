import { Directory, File, Paths } from "expo-file-system";
import { artifactCacheEvictions, type ArtifactCacheEntry, type CachedArtifactFile } from "./artifacts";

const pending = new Map<string, Promise<string>>();

/** A download that turned out larger than the caller would keep. */
export class ArtifactTooLargeError extends Error {
	constructor() {
		super("This file is too large to download to the phone.");
		this.name = "ArtifactTooLargeError";
	}
}

/**
 * An artifact copied into the app's cache, for a viewer that reads a local file
 * (Android's PDF renderer). The entry (artifactCacheEntry) names the file and
 * its version, so an unchanged file opens from the cache and a rewritten one
 * downloads again. The copy is written beside its final name and moved there
 * once complete, so an interrupted download never leaves a cut file behind.
 * Once it is in place, the file's older copies go, and the least recently
 * written other files go while the cache is over its cap. Resolves to its URI.
 * A download over `maxBytes` (when the server did not say its size beforehand)
 * is deleted and rejects with ArtifactTooLargeError, so it is never kept.
 */
export function downloadArtifact(source: { uri: string; headers?: Record<string, string> }, entry: ArtifactCacheEntry, { maxBytes }: { maxBytes?: number } = {}): Promise<string> {
	const inFlight = pending.get(entry.name);
	if (inFlight) return inFlight;
	const task = (async () => {
		const directory = new Directory(Paths.cache, "artifacts");
		if (!directory.exists) directory.create({ intermediates: true, idempotent: true });
		const target = new File(directory, entry.name);
		if (!(target.exists && target.size > 0)) {
			const partial = new File(directory, `${entry.name}.part`);
			if (partial.exists) partial.delete();
			const downloaded = await File.downloadFileAsync(source.uri, partial, { headers: source.headers ?? {}, idempotent: true });
			if (maxBytes !== undefined && (downloaded.size ?? 0) > maxBytes) {
				downloaded.delete();
				throw new ArtifactTooLargeError();
			}
			if (target.exists) target.delete();
			downloaded.move(target);
		}
		pruneCache(directory, entry);
		return target.uri;
	})().finally(() => pending.delete(entry.name));
	pending.set(entry.name, task);
	return task;
}

/**
 * Deletes the cached copy of an entry, so the next open downloads it again:
 * for a copy the viewer could not read (a cut or corrupt file would otherwise
 * be served from the cache on every Try again). A download in progress stays.
 */
export function discardArtifact(entry: ArtifactCacheEntry): void {
	if (pending.has(entry.name)) return;
	try {
		const file = new File(new Directory(Paths.cache, "artifacts"), entry.name);
		if (file.exists) file.delete();
	} catch {
		// Nothing cached, or it cannot be removed now: the next download replaces it.
	}
}

/** Applies artifactCacheEvictions to the cache directory. Never fails the download it follows. */
function pruneCache(directory: Directory, keep: ArtifactCacheEntry): void {
	try {
		const files = directory.list().filter((item): item is File => item instanceof File);
		const facts: CachedArtifactFile[] = files.map((file) => {
			const info = file.info();
			return { name: file.name, size: info.size ?? 0, modified: info.modificationTime ?? 0 };
		});
		const evicted = new Set(artifactCacheEvictions(facts, keep, (name) => pending.has(name)));
		for (const file of files) if (evicted.has(file.name)) file.delete();
	} catch {
		// A cache that cannot be listed or trimmed now is trimmed after the next download.
	}
}
