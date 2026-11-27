import * as Updates from "expo-updates";
import { usePathname } from "expo-router";
import { useEffect, useRef } from "react";
import { AppState } from "react-native";
import { checkAndDownload, isSetupRoute, latestTime, onForeground, shouldOfferUpdate } from "./updates";
import { showUpdateReady } from "./updateScreenState";

// Headless, mounted once in the root layout. Whenever an update finishes
// downloading — natively at launch, on a resume check, or from Settings — it
// raises the update screen, which restarts only when the user taps Restart —
// except during onboarding or pairing, where the offer waits until they leave.
// Each return to the app checks again once the last check is stale, and offers
// an update the user put off again once a day.
export function UpdatesManager(): null {
	const { isUpdatePending, downloadedUpdate, lastCheckForUpdateTimeSinceRestart } = Updates.useUpdates();
	const pending = useRef(isUpdatePending);
	pending.current = isUpdatePending;
	// Native checks on every cold start (ON_LOAD) and may still be at it when
	// this mounts, so the launch counts as a check until native reports its own.
	const lastCheckStarted = useRef(Date.now());
	const nativeLastCheck = useRef<number | null>(null);
	nativeLastCheck.current = lastCheckForUpdateTimeSinceRestart?.getTime() ?? null;
	const lastOffered = useRef<string | null>(null);
	const lastOfferedAt = useRef<number | null>(null);
	const updateId = downloadedUpdate?.updateId;
	// Read on every change so an update that lands during onboarding or pairing
	// is offered as soon as the user leaves it.
	const pathname = usePathname();
	const pathnameRef = useRef(pathname);
	pathnameRef.current = pathname;

	useEffect(() => {
		if (!Updates.isEnabled) return;
		if (!shouldOfferUpdate(isUpdatePending, updateId, lastOffered.current, pathname)) return;
		lastOffered.current = updateId ?? "embedded";
		lastOfferedAt.current = Date.now();
		showUpdateReady();
	}, [isUpdatePending, updateId, pathname]);

	useEffect(() => {
		// Disabled in dev builds; every call would reject.
		if (!Updates.isEnabled) return;
		const sub = AppState.addEventListener("change", (state) => {
			if (state !== "active") return;
			const now = Date.now();
			const action = onForeground({
				pending: pending.current,
				lastCheckAt: latestTime(lastCheckStarted.current, nativeLastCheck.current),
				lastOfferedAt: lastOfferedAt.current,
				now,
			});
			if (action === "prompt") {
				if (isSetupRoute(pathnameRef.current)) return;
				lastOfferedAt.current = now;
				showUpdateReady();
			} else if (action === "check") {
				lastCheckStarted.current = now;
				// A new download flips `isUpdatePending` or changes `updateId`, and the
				// effect above offers it.
				void checkAndDownload(Updates).then((outcome) => {
					if (outcome.kind === "error") console.warn("[updates] check failed", outcome.error);
				});
			}
		});
		return () => sub.remove();
	}, []);

	return null;
}
