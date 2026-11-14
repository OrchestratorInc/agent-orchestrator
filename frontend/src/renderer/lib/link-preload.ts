import { linkOrigin, linkPreviewQueryKey, preloadLinkPreview } from "../hooks/useLinkPreview";
import { useUiStore } from "../stores/ui-store";
import { sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import type { WorkspaceSummary } from "../types/workspace";
import { LOCAL_HOST } from "./hosts";
import { isRasterImagePath, isSvgPath } from "./image-files";
import { queryClient } from "./query-client";
import { artifactAtPath } from "../../../../packages/shared/artifacts/deliverables";
import { parseArtifactLink, type ArtifactLinkTarget } from "./session-links";

// Long enough that links scrolled past or streamed in partially never fetch.
const DWELL_MS = 250;
// Leaves the renderer's few daemon connections free for the app's own reads.
const MAX_CONCURRENT = 2;
// A failed preview is not cached, so without this every scroll would ask again.
const RETRY_AFTER_MS = 5 * 60_000;
const MAX_REMEMBERED = 500;

const PULL_REQUEST_PATH = /^\/[^/]+\/[^/]+\/pull\/\d+\/?$/;

/**
 * Whether a link is a plain GitHub pull request URL. Preloading contacts the
 * target without a hover, so it must not reach arbitrary URLs an agent wrote:
 * those can carry data in a query string, or act on a GET.
 */
export function isPullRequestLink(href: string): boolean {
	let url: URL;
	try {
		url = new URL(href);
	} catch {
		return false;
	}
	return url.protocol === "https:"
		&& url.host === "github.com"
		&& !url.username
		&& !url.password
		&& !url.search
		&& PULL_REQUEST_PATH.test(url.pathname);
}

function warmImage(src: string, crossOrigin?: "anonymous") {
	const image = new Image();
	image.referrerPolicy = "no-referrer";
	if (crossOrigin) image.crossOrigin = crossOrigin;
	image.src = src;
}

async function preloadPullRequest(href: string) {
	const preview = await preloadLinkPreview(href);
	// Same request modes as the card, so its images come from the HTTP cache.
	if (preview?.imageUrl) warmImage(preview.imageUrl);
	if (preview?.faviconUrl) warmImage(preview.faviconUrl, "anonymous");
}

function findLocalArtifact(target: ArtifactLinkTarget) {
	const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey);
	const workspace = workspaces?.find((candidate) => candidate.id === target.projectId);
	const session = workspace?.sessions.find((candidate) => candidate.id === target.sessionId);
	if (!session || session.cloud || (session.hostId && session.hostId !== LOCAL_HOST)) return undefined;
	const artifact = artifactAtPath(session.artifactFiles, target.path);
	return artifact ? { artifact, session } : undefined;
}

async function preloadArtifact(href: string, target: ArtifactLinkTarget) {
	const found = findLocalArtifact(target);
	if (!found) {
		// The artifact can reach the workspace list after the message linking it.
		attempted.delete(href);
		return;
	}
	const { artifact, session } = found;
	// The card loads images and pages from the local daemon and revalidates
	// them, so warming would save only a local read.
	if (!artifact.rawUrl || isRasterImagePath(artifact.path) || isSvgPath(artifact.path)) return;
	if (artifact.kind === "html" && artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl)) return;
	await queryClient.prefetchQuery(sessionArtifactFileQueryOptions(session.id, artifact.path, artifact.rawUrl));
}

function preloadTask(href: string): (() => Promise<void>) | undefined {
	if (isPullRequestLink(href)) return () => preloadPullRequest(href);
	const artifact = parseArtifactLink(href);
	return artifact ? () => preloadArtifact(href, artifact) : undefined;
}

type Watcher = { href: string; run: () => Promise<void>; dwell?: number };

const watchers = new Map<Element, Watcher>();
const queue = new Set<Watcher>();
const attempted = new Map<string, number>();
let observer: IntersectionObserver | undefined;
let active = 0;

function attemptedRecently(href: string): boolean {
	const at = attempted.get(href);
	return at !== undefined && Date.now() - at < RETRY_AFTER_MS;
}

function rememberAttempt(href: string) {
	attempted.delete(href);
	attempted.set(href, Date.now());
	if (attempted.size > MAX_REMEMBERED) attempted.delete(attempted.keys().next().value as string);
}

function drainQueue() {
	for (const watcher of queue) {
		if (active >= MAX_CONCURRENT) return;
		queue.delete(watcher);
		if (attemptedRecently(watcher.href)) continue;
		rememberAttempt(watcher.href);
		active += 1;
		void watcher.run().catch(() => undefined).finally(() => {
			active -= 1;
			drainQueue();
		});
	}
}

function arm(watcher: Watcher) {
	if (watcher.dwell !== undefined || queue.has(watcher) || attemptedRecently(watcher.href)) return;
	watcher.dwell = window.setTimeout(() => {
		watcher.dwell = undefined;
		queue.add(watcher);
		drainQueue();
	}, DWELL_MS);
}

function disarm(watcher: Watcher) {
	window.clearTimeout(watcher.dwell);
	watcher.dwell = undefined;
	queue.delete(watcher);
}

function onIntersection(entries: IntersectionObserverEntry[]) {
	for (const entry of entries) {
		const watcher = watchers.get(entry.target);
		if (watcher) (entry.isIntersecting ? arm : disarm)(watcher);
	}
}

// An IP literal, a single label (localhost, an intranet name) or a local
// suffix names a machine, not a public site with an icon worth asking for.
const MACHINE_HOST = /^(\[.*\]|[\d.]+|[^.]+|.+\.(local|localhost|internal|lan|home\.arpa))$/i;
// Favicons load as soon as a link appears, so they get their own small queue
// rather than waiting behind the hover-card warmers.
const MAX_CONCURRENT_FAVICONS = 3;
const DRAFT_SETTLE_MS = 400;
const DRAFT_URL = /\bhttps?:\/\/[^\s<>"'`)\]]+/gi;

/**
 * The HTTPS site root a link's favicon is read from. Only the root is asked
 * for, never the link: its path and query can carry data or act on a GET, as
 * above. An http link's root is asked for over HTTPS, never in the clear.
 */
function faviconOrigin(href: string): string | undefined {
	let url: URL;
	try {
		url = new URL(href);
	} catch {
		return undefined;
	}
	if (url.username || url.password || MACHINE_HOST.test(url.hostname)) return undefined;
	return linkOrigin(href);
}

const faviconQueue = new Set<string>();
let faviconsActive = 0;

async function preloadFavicon(origin: string) {
	const preview = await preloadLinkPreview(origin);
	// Same request mode as the icon's first load, so it comes from the HTTP cache.
	const icon = (useUiStore.getState().resolvedTheme === "dark" && preview?.faviconDarkUrl) || preview?.faviconUrl;
	if (icon) warmImage(icon, "anonymous");
}

function drainFavicons() {
	for (const origin of faviconQueue) {
		if (faviconsActive >= MAX_CONCURRENT_FAVICONS) return;
		faviconQueue.delete(origin);
		if (attemptedRecently(origin)) continue;
		rememberAttempt(origin);
		faviconsActive += 1;
		void preloadFavicon(origin).catch(() => undefined).finally(() => {
			faviconsActive -= 1;
			drainFavicons();
		});
	}
}

function queueFavicon(origin: string) {
	if (attemptedRecently(origin) || queryClient.getQueryData(linkPreviewQueryKey(origin))) return;
	faviconQueue.add(origin);
	drainFavicons();
}

/** Whether a link's site icon can be asked for at all (a public web site). */
export function canLoadLinkFavicon(href: string): boolean {
	return faviconOrigin(href) !== undefined;
}

/** Loads a web link's site icon now, once per site, for a link on screen. */
export function preloadLinkFavicon(href: string) {
	const origin = faviconOrigin(href);
	if (origin) queueFavicon(origin);
}

/**
 * Warms the site icons of the links typed into a draft, so a sent link shows
 * its icon at once. A link erased before its turn in the queue is dropped; one
 * already asked for finishes, since the daemon caches it either way.
 */
export function createDraftFaviconWarmer() {
	let origins = new Set<string>();
	let timer: number | undefined;
	const apply = (text: string) => {
		const next = new Set<string>();
		for (const [url] of text.matchAll(DRAFT_URL)) {
			const origin = faviconOrigin(url);
			if (origin) next.add(origin);
		}
		for (const origin of next) if (!origins.has(origin)) queueFavicon(origin);
		for (const origin of origins) if (!next.has(origin)) faviconQueue.delete(origin);
		origins = next;
	};
	return {
		update(text: string) {
			window.clearTimeout(timer);
			timer = window.setTimeout(() => apply(text), DRAFT_SETTLE_MS);
		},
		dispose() {
			window.clearTimeout(timer);
			for (const origin of origins) faviconQueue.delete(origin);
			origins.clear();
		},
	};
}

/**
 * Warms a link's hover card once the link has stayed on screen, so the card
 * opens with its content. Any other link keeps loading on hover and costs
 * nothing here. Returns the function that stops watching the element.
 */
export function observeLinkForPreload(element: Element | null, href: string | undefined): (() => void) | undefined {
	if (!element || !href || typeof IntersectionObserver === "undefined") return undefined;
	const run = preloadTask(href);
	if (!run) return undefined;
	const watcher: Watcher = { href, run };
	watchers.set(element, watcher);
	observer ??= new IntersectionObserver(onIntersection);
	observer.observe(element);
	return () => {
		disarm(watcher);
		watchers.delete(element);
		observer?.unobserve(element);
	};
}
