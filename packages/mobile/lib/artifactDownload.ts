import { Directory, File, Paths } from "expo-file-system";

const pending = new Map<string, Promise<string>>();

/**
 * An artifact copied into the app's cache, for a viewer that reads a local file
 * (Android's PDF renderer). `name` (artifactCacheName) is per file and version,
 * so an unchanged file opens from the cache and a rewritten one downloads again.
 * The copy is written beside its final name and moved there once complete, so
 * an interrupted download never leaves a cut file behind. Resolves to its URI.
 */
export function downloadArtifact(source: { uri: string; headers: Record<string, string> }, name: string): Promise<string> {
	const inFlight = pending.get(name);
	if (inFlight) return inFlight;
	const task = (async () => {
		const directory = new Directory(Paths.cache, "artifacts");
		if (!directory.exists) directory.create({ intermediates: true, idempotent: true });
		const target = new File(directory, name);
		if (target.exists && target.size > 0) return target.uri;
		const partial = new File(directory, `${name}.part`);
		if (partial.exists) partial.delete();
		const downloaded = await File.downloadFileAsync(source.uri, partial, { headers: source.headers, idempotent: true });
		if (target.exists) target.delete();
		downloaded.move(target);
		return target.uri;
	})().finally(() => pending.delete(name));
	pending.set(name, task);
	return task;
}
