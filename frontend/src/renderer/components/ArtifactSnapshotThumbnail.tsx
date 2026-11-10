import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { ArtifactSnapshotJob } from "../../shared/artifact-snapshot";
import { aoBridge } from "../lib/bridge";
import type { ArtifactRow } from "../lib/artifacts";
import { LOCAL_HOST } from "../lib/hosts";
import { ArtifactKindIcon, ArtifactThumbnail } from "./ArtifactThumbnail";

/** Decoded images kept for cards that scroll away and come back; the bytes themselves live on disk. */
const MEMORY_LIMIT = 300;
/** A request the queue pushed out is made again after this long. */
const RETRY_DROPPED_MS = 1_500;

type Remembered = { url: string; users: number };
// A Map iterates in insertion order, so re-inserting on every use keeps it least recently used first.
const images = new Map<string, Remembered>();

function remember(key: string, url: string): Remembered {
	const entry = images.get(key) ?? { url, users: 0 };
	images.delete(key);
	images.set(key, entry);
	for (const [oldest, old] of images) {
		if (images.size <= MEMORY_LIMIT) break;
		if (old.users > 0) continue;
		images.delete(oldest);
		URL.revokeObjectURL(old.url);
	}
	return entry;
}

/** A card showing the image holds it, so eviction never revokes a URL that is on screen, and uses refresh its place. */
function hold(key: string, entry: Remembered): () => void {
	images.delete(key);
	images.set(key, entry);
	entry.users += 1;
	return () => {
		entry.users -= 1;
	};
}

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

type Result = { key: string; url?: string; failed?: boolean };

/**
 * A card's preview as a cached image: the file on disk when there is one, else
 * the one main renders once, and never a live render of the file. An artifact
 * main cannot render (another host's, or a render that failed) falls back to
 * the live preview so the card still shows something. While one is rendering
 * the card shows its kind's icon.
 */
export function ArtifactSnapshotThumbnail({ row, previewLive = true }: { row: ArtifactRow; previewLive?: boolean }) {
	const container = useRef<HTMLSpanElement>(null);
	const appearance = useSyncExternalStore(subscribeToTheme, themeSnapshot);
	const [near, setNear] = useState(false);
	const [inView, setInView] = useState(false);
	const [attempt, setAttempt] = useState(0);
	const [result, setResult] = useState<Result>();
	const job = snapshotJob(row, appearance);
	const key = job ? [job.cloud?.orgId ?? "", job.sessionId, job.path, job.updatedAt, appearance].join("|") : "";
	const cached = key ? images.get(key) : undefined;
	const priority = inView ? 1 : 0;
	const priorityRef = useRef(priority);
	priorityRef.current = priority;
	// Whether a render request for the current key is waiting on main.
	const requested = useRef(false);

	useEffect(() => {
		const element = container.current;
		if (!element || cached) return;
		// "Near" starts the request; "in view" puts it ahead of cards only inside the margin.
		const nearObserver = new IntersectionObserver(([entry]) => {
			if (entry?.isIntersecting) setNear(true);
		}, { rootMargin: "300px" });
		const viewObserver = new IntersectionObserver(([entry]) => setInView(Boolean(entry?.isIntersecting)));
		nearObserver.observe(element);
		viewObserver.observe(element);
		return () => {
			nearObserver.disconnect();
			viewObserver.disconnect();
		};
	}, [cached]);

	useEffect(() => {
		if (!job || !near || cached) return;
		let current = true;
		let retry: ReturnType<typeof setTimeout> | undefined;
		const finish = (bytes: Uint8Array | null | "dropped") => {
			if (!current) return;
			requested.current = false;
			if (bytes === "dropped") {
				// The queue was full: not a failure. Ask again once it has had time to drain.
				retry = setTimeout(() => setAttempt((count) => count + 1), RETRY_DROPPED_MS);
				return;
			}
			if (!bytes) {
				setResult({ key, failed: true });
				return;
			}
			const entry = remember(key, URL.createObjectURL(new Blob([bytes as BlobPart], { type: "image/webp" })));
			setResult({ key, url: entry.url });
		};
		aoBridge.artifactSnapshot.get(job)
			.then((bytes) => {
				if (bytes || !current) return bytes;
				requested.current = true;
				return aoBridge.artifactSnapshot.request(job, priorityRef.current);
			})
			.then(finish, () => finish(null));
		return () => {
			current = false;
			clearTimeout(retry);
		};
		// The job is a pure function of the key; listing it would rerun this on every row object.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [key, near, cached, attempt]);

	// Once a card scrolls into view, a request still waiting in the queue moves ahead of those below the fold.
	useEffect(() => {
		if (!job || !requested.current || !inView) return;
		void aoBridge.artifactSnapshot.request(job, 1).catch(() => undefined);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [inView]);

	const entry = cached ?? (result?.key === key && result.url ? images.get(key) : undefined);
	useEffect(() => (entry ? hold(key, entry) : undefined), [key, entry]);
	if (!job) return <ArtifactThumbnail row={row} previewLive={previewLive} />;
	if (result?.key === key && result.failed && !entry) return <ArtifactThumbnail row={row} previewLive={previewLive} />;
	return (
		<span ref={container} className="pointer-events-none absolute inset-0 block overflow-hidden bg-[var(--solid-muted,var(--muted))]" aria-hidden="true">
			{entry ? (
				<img alt="" className="size-full object-cover object-top" decoding="async" draggable={false} src={entry.url} />
			) : (
				<span className="absolute inset-0 grid place-items-center text-passive">
					<ArtifactKindIcon className="size-7" row={row} />
				</span>
			)}
		</span>
	);
}
