import AsyncStorage from "@react-native-async-storage/async-storage";
import * as Updates from "expo-updates";

import { decodeResumeRoute, encodeResumeRoute, RESUME_ROUTE_KEY } from "./updateResume";

// Storage side of updateResume.ts. A page path is not a secret, so AsyncStorage.

// The last page the user was on, kept current by UpdateRouteKeeper. Null on the
// board. Module state rather than React state: the reload reads it from outside
// any component.
let currentPage: string | null = null;

export function setCurrentPage(href: string | null): void {
	currentPage = href;
}

/**
 * Restart into the downloaded update, reopening the current page afterwards.
 * Used as the `reloadAsync` for `applyUpdate`. A reload that fails clears the
 * saved page again so a later launch does not jump to it.
 */
export async function reloadIntoUpdate(): Promise<void> {
	const page = currentPage;
	try {
		if (page) await AsyncStorage.setItem(RESUME_ROUTE_KEY, encodeResumeRoute(page, Date.now()));
	} catch {
		/* losing the page only means the app reopens on the board */
	}
	try {
		await Updates.reloadAsync();
	} catch (error) {
		await AsyncStorage.removeItem(RESUME_ROUTE_KEY).catch(() => {});
		throw error;
	}
}

/** The page saved by the restart, removed as it is read so it is used at most once. Never throws. */
export async function takeResumeRoute(): Promise<string | null> {
	try {
		const raw = await AsyncStorage.getItem(RESUME_ROUTE_KEY);
		if (raw !== null) await AsyncStorage.removeItem(RESUME_ROUTE_KEY);
		return decodeResumeRoute(raw, Date.now());
	} catch {
		return null;
	}
}
