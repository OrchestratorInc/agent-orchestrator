import { useEffect, useRef, useState } from "react";
import { AppState } from "react-native";
import { releaseCatalogRefreshMs } from "./modelCatalog";

/**
 * While a picker shows Cloud's release list (`showsRelease`), a count that goes
 * up each time the list should be read again, as the desktop reads it
 * (useCloudModelCatalog): after UNVERIFIED_CATALOG_REFRESH_MS, then twice as
 * long each time it is still the release list, up to five minutes; and at once
 * when the app comes back to the foreground, which starts the waits again at
 * the shortest (someone is looking at it now). 0 until the first re-read.
 */
export function useReleaseCatalogRereads(showsRelease: boolean): number {
	const [rereads, setRereads] = useState(0);
	const streak = useRef(0);

	useEffect(() => {
		if (!showsRelease) {
			streak.current = 0;
			return;
		}
		streak.current += 1;
		const timer = setTimeout(() => setRereads((count) => count + 1), releaseCatalogRefreshMs(streak.current));
		return () => clearTimeout(timer);
	}, [rereads, showsRelease]);

	useEffect(() => {
		if (!showsRelease) return;
		let previous = AppState.currentState;
		const subscription = AppState.addEventListener("change", (next) => {
			if (previous !== "active" && next === "active") {
				streak.current = 0;
				setRereads((count) => count + 1);
			}
			previous = next;
		});
		return () => subscription.remove();
	}, [showsRelease]);

	return rereads;
}
