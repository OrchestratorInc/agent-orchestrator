import { useEffect, useLayoutEffect, useRef } from "react";
import { artifactCountBucket } from "../lib/artifact-telemetry";
import { captureRendererEvent } from "../lib/telemetry";

/**
 * Sends ao.artifacts.list_opened once per open of an artifact list: the
 * Artifacts page, or a session's Artifacts tab while the inspector shows it.
 * The clock starts when the list becomes visible and stops on the first
 * painted card. A list closed before it settles reports `left`; re-renders,
 * refetches and later count changes send nothing.
 */
export function useArtifactListTelemetry({ surface, visible, settled, failed = false, count, hasCards }: {
	surface: "page" | "tab";
	visible: boolean;
	settled: boolean;
	failed?: boolean;
	count: number;
	hasCards: boolean;
}): void {
	const startedAt = useRef<number | null>(null);
	const sent = useRef(false);
	// Runs before the effect below, in the same commit that shows the list.
	useLayoutEffect(() => {
		if (visible && startedAt.current === null) startedAt.current = performance.now();
	}, [visible]);

	useEffect(() => {
		const start = startedAt.current;
		if (!visible || sent.current || start === null || !settled) return;
		sent.current = true;
		if (failed || !hasCards) {
			void captureRendererEvent("ao.artifacts.list_opened", {
				surface,
				outcome: failed ? "failed" : "empty",
				artifact_count_bucket: failed ? undefined : artifactCountBucket(count),
			});
			return;
		}
		// The cards are committed; the next frame is when they are on screen.
		requestAnimationFrame(() => {
			void captureRendererEvent("ao.artifacts.list_opened", {
				surface,
				outcome: "loaded",
				artifact_count_bucket: artifactCountBucket(count),
				time_to_first_card_ms: Math.round(performance.now() - start),
			});
		});
	}, [visible, settled, failed, count, hasCards, surface]);

	useEffect(() => () => {
		if (sent.current || startedAt.current === null) return;
		sent.current = true;
		void captureRendererEvent("ao.artifacts.list_opened", { surface, outcome: "left" });
	}, []); // eslint-disable-line react-hooks/exhaustive-deps
}
