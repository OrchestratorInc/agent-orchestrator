import AsyncStorage from "@react-native-async-storage/async-storage";

// Where projects were last made on each machine, so the next one starts there:
// the folders the last few existing-folder projects sat in, which the folder
// browser opens at and offers as shortcuts, and where the last clone or new
// project went. Kept per machine because a path means nothing on another one.

export type ProjectPlaces = {
	/** Folders that held recently added projects, most recent first. */
	recent: string[];
	/** Where the last clone or new project was made. */
	destination?: string;
};

export const RECENT_PLACES_MAX = 4;

const KEY_PREFIX = "ao.projectPlaces.";
const cache = new Map<string, ProjectPlaces>();

const storageKey = (hostId: string) => `${KEY_PREFIX}${hostId}`;

/** `path` first, without its earlier mention, keeping at most `max`. */
export function withRecentPlace(recent: readonly string[], path: string, max = RECENT_PLACES_MAX): string[] {
	return [path, ...recent.filter((item) => item !== path)].slice(0, max);
}

/** The folder a path sits in, for either separator; the path itself at a root. */
export function parentFolder(path: string): string {
	const trimmed = path.replace(/[\\/]+$/, "");
	const cut = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
	if (cut < 0) return path;
	if (cut === 0) return trimmed.slice(0, 1);
	const parent = trimmed.slice(0, cut);
	// A Windows drive root keeps its separator: `C:` alone is relative to the drive's current folder.
	return /^[A-Za-z]:$/.test(parent) ? `${parent}${trimmed[cut]}` : parent;
}

function parsePlaces(raw: string | null): ProjectPlaces {
	if (!raw) return { recent: [] };
	try {
		const data = JSON.parse(raw) as Partial<ProjectPlaces> | null;
		const recent = Array.isArray(data?.recent) ? data.recent.filter((item): item is string => typeof item === "string" && item.length > 0).slice(0, RECENT_PLACES_MAX) : [];
		const destination = typeof data?.destination === "string" && data.destination ? data.destination : undefined;
		return destination ? { recent, destination } : { recent };
	} catch {
		return { recent: [] };
	}
}

export async function loadProjectPlaces(hostId: string): Promise<ProjectPlaces> {
	const cached = cache.get(hostId);
	if (cached) return cached;
	const stored = parsePlaces(await AsyncStorage.getItem(storageKey(hostId)).catch(() => null));
	// A remember that landed while the read was in flight is newer.
	if (!cache.has(hostId)) cache.set(hostId, stored);
	return cache.get(hostId)!;
}

/** After a project is made: the folder it sat in, or where it was cloned or created. */
export async function rememberProjectPlace(hostId: string, place: { folder?: string; destination?: string }): Promise<void> {
	const current = await loadProjectPlaces(hostId);
	const next: ProjectPlaces = {
		recent: place.folder ? withRecentPlace(current.recent, parentFolder(place.folder)) : current.recent,
		...(place.destination ?? current.destination ? { destination: place.destination ?? current.destination } : {}),
	};
	cache.set(hostId, next);
	await AsyncStorage.setItem(storageKey(hostId), JSON.stringify(next)).catch(() => {});
}

/** Test seam: forget what was read. */
export function resetProjectPlacesCache(): void {
	cache.clear();
}
