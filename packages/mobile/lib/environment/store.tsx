import AsyncStorage from "@react-native-async-storage/async-storage";
import type { EnvironmentKind } from "./types";

const KEY = "ao.environment";

/** Which environment is active, defaulting (and falling back) to local. */
export async function loadEnvironment(): Promise<EnvironmentKind> {
	try {
		const raw = await AsyncStorage.getItem(KEY);
		return raw === "cloud" ? "cloud" : "local";
	} catch {
		return "local";
	}
}

export async function saveEnvironment(kind: EnvironmentKind): Promise<void> {
	try {
		await AsyncStorage.setItem(KEY, kind);
	} catch {
		// A failed write costs the user one re-selection; it must not crash.
	}
}
