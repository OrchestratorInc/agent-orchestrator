import { useEffect, useState } from "react";
import { getPreview } from "../api";
import { isConfigured, type ServerConfig } from "../config";

/** How often the preview detector is asked again while a session is open. */
const PREVIEW_POLL_MS = 5000;

/**
 * What counts as a live preview: any file the daemon surfaces (an .html build, or
 * a generated doc like plan.md / a report) EXCEPT a repo's README, which the
 * detector's markdown fallback always matches on a fresh checkout. Filtering the
 * README out keeps the globe's green dot meaningful: it means "there's something
 * the agent produced to view", not just "this repo has a README".
 */
export function isLivePreview(preview: { entry: string } | null): boolean {
	if (!preview) return false;
	const base = preview.entry.split("/").pop() ?? "";
	return !/^readme\.(md|markdown)$/i.test(base);
}

/**
 * Whether the session has something to preview, polled from the daemon's
 * on-demand detector while the session is open, just to keep the header's globe
 * current. Nothing opens the preview on its own: the detector's markdown
 * fallback matches a repo README, so popping it open would surface a blank or
 * unbuilt page. Chat and the terminal share it, so their globes agree.
 */
export function useSessionPreview(config: ServerConfig | null | undefined, sessionId: string, enabled = true): boolean {
	// The entry itself, not an object around it: an answer that has not changed
	// then leaves the screen alone, where a new object every poll re-rendered
	// the whole chat every five seconds.
	const [entry, setEntry] = useState<string | null>(null);
	useEffect(() => {
		if (!enabled || !config || !isConfigured(config)) return;
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;
		const tick = async () => {
			try {
				const found = await getPreview(config, sessionId);
				if (cancelled) return;
				setEntry(found ? found.entry : null);
			} catch {
				/* transient - keep polling */
			}
			if (!cancelled) timer = setTimeout(tick, PREVIEW_POLL_MS);
		};
		void tick();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [config, enabled, sessionId]);
	return isLivePreview(entry === null ? null : { entry });
}
