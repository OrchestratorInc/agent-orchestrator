import { sourceKey, type SourceRef } from "./environment/scopedBoard";

/** A generic compose must not silently prefer one host over another. */
export function initialSpawnDestination(route: SourceRef | null | undefined, available: readonly SourceRef[], previous?: SourceRef | null): SourceRef | null {
	if (route) return available.find((source) => sourceKey(source) === sourceKey(route)) ?? null;
	if (previous) {
		const saved = available.find((source) => sourceKey(source) === sourceKey(previous));
		if (saved) return saved;
	}
	return available.length === 1 ? available[0] : null;
}

export function canSubmitSpawn(
	destination: SourceRef | null,
	projectId: string | null,
	harness: string,
	sourceFor: (source: SourceRef) => unknown,
): boolean {
	return !!destination && !!projectId && !!harness && !!sourceFor(destination);
}

export function spawnRequestIsCurrent(generation: number, currentGeneration: number, requested: SourceRef, selected: SourceRef | null): boolean {
	return generation === currentGeneration && selected !== null && sourceKey(requested) === sourceKey(selected);
}
