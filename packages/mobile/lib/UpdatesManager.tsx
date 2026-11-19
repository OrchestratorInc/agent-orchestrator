import * as Updates from "expo-updates";
import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import { checkAndDownload, onForeground, shouldOfferUpdate } from "./updates";
import { showUpdateReady } from "./updateScreenState";

// Headless, mounted once in the root layout. Whenever an update finishes
// downloading — natively at launch, on a resume check, or from Settings — it
// raises the update screen, which restarts only when the user taps Restart.
// After the app has been in the background a while, a resume offers a pending
// update again (if it was put off) or checks for a new one in the background.
export function UpdatesManager(): null {
	const { isUpdatePending, downloadedUpdate } = Updates.useUpdates();
	const pending = useRef(isUpdatePending);
	pending.current = isUpdatePending;
	const backgroundedAt = useRef<number | null>(null);
	const lastOffered = useRef<string | null>(null);
	const updateId = downloadedUpdate?.updateId;

	useEffect(() => {
		if (!Updates.isEnabled) return;
		if (!shouldOfferUpdate(isUpdatePending, updateId, lastOffered.current)) return;
		lastOffered.current = updateId ?? "embedded";
		showUpdateReady();
	}, [isUpdatePending, updateId]);

	useEffect(() => {
		// Disabled in dev builds; every call would reject.
		if (!Updates.isEnabled) return;
		const sub = AppState.addEventListener("change", (state) => {
			if (state === "background") {
				backgroundedAt.current = Date.now();
				return;
			}
			if (state !== "active") return;
			const action = onForeground(pending.current, backgroundedAt.current, Date.now());
			backgroundedAt.current = null;
			if (action === "prompt") {
				showUpdateReady();
			} else if (action === "check") {
				// A new download flips `isUpdatePending`, and the effect above offers it.
				void checkAndDownload(Updates).then((outcome) => {
					if (outcome.kind === "error") console.warn("[updates] check failed", outcome.error);
				});
			}
		});
		return () => sub.remove();
	}, []);

	return null;
}
