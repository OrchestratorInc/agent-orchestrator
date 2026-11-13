import { useEffect, useReducer, useRef, useSyncExternalStore } from "react";
import { type ArtifactSnapshotJob, artifactSnapshotUrl } from "../../shared/artifact-snapshot";
import { aoBridge } from "../lib/bridge";
import type { ArtifactRow } from "../lib/artifacts";
import { LOCAL_HOST } from "../lib/hosts";
import { ArtifactThumbnail } from "./ArtifactThumbnail";

/** How long a live card stays on screen with its preview drawn before its snapshot is taken. */
const SNAPSHOT_AFTER_MS = 1_500;

// Snapshot URLs that answered "not cached", so a card that comes back goes straight to its live
// preview; and those rendered since, whose URL gets a fresh query so no failed load is reused.
const missing = new Set<string>();
const rendered = new Set<string>();

// One observer for every card on the page, not one each.
const themeListeners = new Set<() => void>();
let themeObserver: MutationObserver | undefined;

function subscribeToTheme(onChange: () => void) {
	themeListeners.add(onChange);
	themeObserver ??= new MutationObserver(() => themeListeners.forEach((listener) => listener()));
	if (themeListeners.size === 1) themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "data-style-theme"] });
	return () => {
		themeListeners.delete(onChange);
		if (themeListeners.size === 0) themeObserver?.disconnect();
	};
}

const themeSnapshot = () => `${document.documentElement.dataset.theme === "light" ? "light" : "dark"}|${document.documentElement.dataset.styleTheme ?? ""}`;

/**
 * Only this computer's and Cloud sessions' artifacts can be rendered by the hidden window. Another host's
 * are reached through URLs that carry a capability the window must not hold, so those cards render live.
 * Main enforces the same rule on the job it receives.
 */
function snapshotJob(row: ArtifactRow, appearance: string): ArtifactSnapshotJob | undefined {
	if (row.hostId && row.hostId !== LOCAL_HOST) return undefined;
	const [theme, styleTheme] = appearance.split("|") as ["light" | "dark", string];
	return {
		sessionId: row.sessionId,
		path: row.path,
		name: row.name,
		kind: row.kind,
		updatedAt: row.updatedAt,
		inlineUrl: row.inlineUrl,
		rawUrl: row.rawUrl,
		previewUrl: row.previewUrl,
		sessionTitle: row.sessionTitle,
		cloud: row.cloud ? { orgId: row.cloud.orgId } : undefined,
		hostId: row.hostId,
		theme,
		styleTheme,
	};
}

/**
 * A card's preview as a cached image when there is one: a plain <img> main serves from disk,
 * with no script and no live render. A card with none yet draws live, exactly as without the
 * cache, so a first visit is never slower; once it has been on screen with its preview drawn,
 * main renders the snapshot in the background for every later visit.
 */
export function ArtifactSnapshotThumbnail({ row, previewLive = true }: { row: ArtifactRow; previewLive?: boolean }) {
	const appearance = useSyncExternalStore(subscribeToTheme, themeSnapshot);
	const [, missed] = useReducer((count: number) => count + 1, 0);
	const job = snapshotJob(row, appearance);
	const url = job ? artifactSnapshotUrl(job) : "";
	if (!job || missing.has(url)) return <LiveThumbnail job={job} previewLive={previewLive} row={row} url={url} />;
	return (
		<span className="pointer-events-none absolute inset-0 block overflow-hidden bg-[var(--solid-muted,var(--muted))]" aria-hidden="true">
			<img
				alt=""
				className="size-full object-cover object-top"
				draggable={false}
				onError={() => {
					missing.add(url);
					missed();
				}}
				src={rendered.has(url) ? `${url}&r=1` : url}
			/>
		</span>
	);
}

/** The live preview, which asks for its snapshot once it has been drawn on screen for a while. */
function LiveThumbnail({ row, previewLive, job, url }: { row: ArtifactRow; previewLive: boolean; job?: ArtifactSnapshotJob; url: string }) {
	const container = useRef<HTMLSpanElement>(null);

	useEffect(() => {
		const element = container.current;
		if (!job || !element) return;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const check = () => {
			const state = element.querySelector("[data-preview-state]")?.getAttribute("data-preview-state");
			// Wait for the live preview, so the snapshot never competes with it.
			if (state === "loading") {
				timer = setTimeout(check, SNAPSHOT_AFTER_MS);
				return;
			}
			// A card past the page's live-preview limit shows its icon; its snapshot is the only preview it gets.
			if (state !== "content" && previewLive) return;
			observer.disconnect();
			void aoBridge.artifactSnapshot.request(job).then((ok) => {
				if (!ok) return;
				missing.delete(url);
				rendered.add(url);
			}, () => undefined);
		};
		// Only a card that stays in view: a fast scroll past a card does not render it.
		const observer = new IntersectionObserver(([entry]) => {
			clearTimeout(timer);
			if (entry?.isIntersecting) timer = setTimeout(check, SNAPSHOT_AFTER_MS);
		});
		observer.observe(element);
		return () => {
			observer.disconnect();
			clearTimeout(timer);
		};
		// The job is a pure function of the URL; listing it would rerun this on every row object.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [url, previewLive]);

	return (
		<span ref={container} className="pointer-events-none absolute inset-0 block">
			<ArtifactThumbnail row={row} previewLive={previewLive} />
		</span>
	);
}
