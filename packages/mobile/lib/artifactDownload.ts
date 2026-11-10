import { Directory, File, Paths } from "expo-file-system";
import { artifactCacheEvictions, type ArtifactCacheEntry, type CachedArtifactFile } from "./artifacts";

const pending = new Map<string, Promise<string>>();

/**
 * An artifact copied into the app's cache, for a viewer that reads a local file
 * (Android's PDF renderer). The entry (artifactCacheEntry) names the file and
 * its version, so an unchanged file opens from the cache and a rewritten one
 * downloads again. The copy is written beside its final name and moved there
 * once complete, so an interrupted download never leaves a cut file behind.
 * Once it is in place, the file's older copies go, and the least recently
 * written other files go while the cache is over its cap. Resolves to its URI.
 */
export function downloadArtifact(source: { uri: string; headers: Record<string, string> }, entry: ArtifactCacheEntry): Promise<string> {
	const inFlight = pending.get(entry.name);
	if (inFlight) return inFlight;
	const task = (async () => {
		const directory = new Directory(Paths.cache, "artifacts");
		if (!directory.exists) directory.create({ intermediates: true, idempotent: true });
		const target = new File(directory, entry.name);
		if (!(target.exists && target.size > 0)) {
			const partial = new File(directory, `${entry.name}.part`);
			if (partial.exists) partial.delete();
			const downloaded = await File.downloadFileAsync(source.uri, partial, { headers: source.headers, idempotent: true });
			if (target.exists) target.delete();
			downloaded.move(target);
		}
		pruneCache(directory, entry);
		return target.uri;
	})().finally(() => pending.delete(entry.name));
	pending.set(entry.name, task);
	return task;
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
