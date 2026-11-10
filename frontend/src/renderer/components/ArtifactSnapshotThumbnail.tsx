import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { ArtifactSnapshotJob } from "../../shared/artifact-snapshot";
import { aoBridge } from "../lib/bridge";
import type { ArtifactRow } from "../lib/artifacts";
import { ArtifactThumbnail } from "./ArtifactThumbnail";

/** Decoded images kept for cards that scroll away and come back; the bytes themselves live on disk. */
const MEMORY_LIMIT = 300;
const images = new Map<string, string>();

function remember(key: string, url: string) {
	images.delete(key);
	images.set(key, url);
	for (const [oldest, oldUrl] of images) {
		if (images.size <= MEMORY_LIMIT) break;
		images.delete(oldest);
		URL.revokeObjectURL(oldUrl);
	}
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

function isLoopbackUrl(url: string | undefined): boolean {
	if (!url) return true;
	try {
		const { hostname } = new URL(url);
		return hostname === "127.0.0.1" || hostname === "localhost" || hostname.endsWith(".localhost");
	} catch {
		return false;
	}
}

/** Local and Cloud artifacts can be rendered by the hidden window; another host's carry a capability token it must not hold. */
function snapshotJob(row: ArtifactRow, appearance: string): ArtifactSnapshotJob | undefined {
	if (!row.cloud && !(isLoopbackUrl(row.rawUrl) && isLoopbackUrl(row.inlineUrl) && isLoopbackUrl(row.previewUrl))) return undefined;
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
		theme,
		styleTheme,
	};
}

type Result = { key: string; url?: string; failed?: boolean };

/**
 * A card's preview as a cached image: the file on disk when there is one, else
 * the one main renders once, and never a live render of the file. An artifact
 * main cannot render (another host's, or a render that failed) falls back to
 * the live preview so the card still shows something.
 */
export function ArtifactSnapshotThumbnail({ row, previewLive = true }: { row: ArtifactRow; previewLive?: boolean }) {
	const container = useRef<HTMLSpanElement>(null);
	const appearance = useSyncExternalStore(subscribeToTheme, themeSnapshot);
	const [near, setNear] = useState(false);
	const [result, setResult] = useState<Result>();
	const job = snapshotJob(row, appearance);
	const key = job ? [job.cloud?.orgId ?? "", job.sessionId, job.path, job.updatedAt, appearance].join("|") : "";
	const cached = key ? images.get(key) : undefined;

	useEffect(() => {
		const element = container.current;
		if (!element || cached) return;
		const observer = new IntersectionObserver(([entry]) => {
			if (entry?.isIntersecting) setNear(true);
		}, { rootMargin: "300px" });
		observer.observe(element);
		return () => observer.disconnect();
	}, [cached]);

	useEffect(() => {
		if (!job || !near || cached) return;
		let current = true;
		const finish = (bytes: Uint8Array | null) => {
			if (!current) return;
			if (!bytes) {
				setResult({ key, failed: true });
				return;
			}
			const url = URL.createObjectURL(new Blob([bytes as BlobPart], { type: "image/webp" }));
			remember(key, url);
			setResult({ key, url });
		};
		aoBridge.artifactSnapshot.get(job)
			.then((bytes) => bytes ?? aoBridge.artifactSnapshot.request(job))
			.then(finish, () => finish(null));
		return () => {
			current = false;
		};
		// The job is a pure function of the key; listing it would rerun this on every row object.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [key, near, cached]);

	if (!job) return <ArtifactThumbnail row={row} previewLive={previewLive} />;
	const url = cached ?? (result?.key === key ? result.url : undefined);
	if (result?.key === key && result.failed && !url) return <ArtifactThumbnail row={row} previewLive={previewLive} />;
	return (
		<span ref={container} className="pointer-events-none absolute inset-0 block overflow-hidden bg-[var(--solid-muted,var(--muted))]" aria-hidden="true">
			{url ? <img alt="" className="size-full object-cover object-top" decoding="async" draggable={false} src={url} /> : null}
		</span>
	);
}
