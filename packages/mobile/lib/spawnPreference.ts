import AsyncStorage from "@react-native-async-storage/async-storage";
import type { SourceRef } from "./environment/scopedBoard";

const KEY = "ao.spawnPreference";

export type SpawnPreference = { source: SourceRef; projectId: string | null };

export async function loadSpawnPreference(): Promise<SpawnPreference | null> {
	try {
		const raw = await AsyncStorage.getItem(KEY);
		if (!raw) return null;
		const value: unknown = JSON.parse(raw);
		if (!value || typeof value !== "object") return null;
		const { source, projectId } = value as Record<string, unknown>;
		if (!source || typeof source !== "object") return null;
		const { kind, id } = source as Record<string, unknown>;
		if ((kind !== "local" && kind !== "cloud") || typeof id !== "string" || !id) return null;
		if (projectId !== null && typeof projectId !== "string") return null;
		return { source: { kind, id }, projectId };
	} catch {
		return null;
	}
}

export async function saveSpawnPreference(preference: SpawnPreference): Promise<void> {
	try {
		await AsyncStorage.setItem(KEY, JSON.stringify(preference));
	} catch {
		// A failed preference write should not block spawning.
	}
}
