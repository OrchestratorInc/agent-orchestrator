import { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import { artifactCountBucket } from "../lib/artifact-telemetry";
import { captureRendererEvent } from "../lib/telemetry";

/**
 * Sends ao.artifacts.list_opened once per open of an artifact list: the
 * Artifacts page, or a session's Artifacts tab while the inspector shows it.
 * `open` is the list's surface being open; `showing` is its cards being on
 * screen rather than covered by one open artifact. The clock starts when the
 * cards first show in an open and stops on the first painted card. Closing
 * the list before an outcome reports `left`, and the next open counts again;
 * re-renders, refetches and count changes within an open send nothing.
 */
export function useArtifactListTelemetry({ surface, open = true, showing = true, settled, failed = false, count, hasCards }: {
	surface: "page" | "tab";
	open?: boolean;
	showing?: boolean;
	settled: boolean;
	failed?: boolean;
	count: number;
	hasCards: boolean;
}): void {
	const startedAt = useRef<number | null>(null);
	const sent = useRef(false);
	const frame = useRef<number | null>(null);

	// Ends the current open: a pending paint never reports for a closed list.
	const close = useCallback(() => {
		if (frame.current !== null) cancelAnimationFrame(frame.current);
		frame.current = null;
		if (startedAt.current !== null && !sent.current) {
			void captureRendererEvent("ao.artifacts.list_opened", { surface, outcome: "left" });
		}
		startedAt.current = null;
		sent.current = false;
	}, [surface]);

	// Runs before the effect below, in the same commit that shows the list.
	useLayoutEffect(() => {
		if (!open) close();
		else if (showing && startedAt.current === null) startedAt.current = performance.now();
	}, [open, showing, close]);

	useEffect(() => {
		const start = startedAt.current;
		if (!open || !showing || sent.current || frame.current !== null || start === null || !settled) return;
		if (failed || !hasCards) {
			sent.current = true;
			void captureRendererEvent("ao.artifacts.list_opened", {
				surface,
				outcome: failed ? "failed" : "empty",
				artifact_count_bucket: failed ? undefined : artifactCountBucket(count),
			});
			return;
		}
		// The cards are committed; the next frame is when they are on screen.
		frame.current = requestAnimationFrame(() => {
			frame.current = null;
			sent.current = true;
			void captureRendererEvent("ao.artifacts.list_opened", {
				surface,
				outcome: "loaded",
				artifact_count_bucket: artifactCountBucket(count),
				time_to_first_card_ms: Math.round(performance.now() - start),
			});
		});
	}, [open, showing, settled, failed, count, hasCards, surface]);

	useEffect(() => close, [close]);
}
