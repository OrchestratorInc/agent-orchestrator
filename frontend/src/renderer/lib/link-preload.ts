import { preloadLinkPreview } from "../hooks/useLinkPreview";
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
