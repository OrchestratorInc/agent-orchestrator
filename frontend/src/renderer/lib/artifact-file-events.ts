import type { QueryClient } from "@tanstack/react-query";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { eventStreamBaseUrl, getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { computeSseRetryDelayMs } from "./sse-backoff";

/** How long after the first change an open file is reread. */
const FILE_REFRESH_MS = 150;
/** The soonest the workspace list, which carries each file's modified time, is reread again. */
const LIST_REFRESH_MS = 1_000;
const EVENTSOURCE_CLOSED = 2;

type ArtifactStream = { refs: number; dispose: () => void };

const streams = new Map<string, ArtifactStream>();

/**
 * Keeps a local session's open artifacts current while an agent writes them.
 * The daemon watches the session's artifact directory and sends only
 * invalidation edges; each one rereads the session's artifact files and,
 * throttled, the workspace list, whose modified times reload an open page.
 * Views of the same session share one stream. Returns the unsubscribe.
 */
export function subscribeArtifactFileChanges(sessionId: string, queryClient: QueryClient): () => void {
	let stream = streams.get(sessionId);
	if (!stream) {
		stream = { refs: 0, dispose: openArtifactStream(sessionId, queryClient) };
		streams.set(sessionId, stream);
	}
	stream.refs += 1;
	let released = false;
	return () => {
		if (released) return;
		released = true;
		const current = streams.get(sessionId);
		if (!current) return;
		current.refs -= 1;
		if (current.refs > 0) return;
		current.dispose();
		streams.delete(sessionId);
	};
}

function openArtifactStream(sessionId: string, queryClient: QueryClient): () => void {
	let disposed = false;
	let source: EventSource | undefined;
	let sourceBaseUrl: string | undefined;
	let failures = 0;
	let retry: ReturnType<typeof setTimeout> | undefined;
	let fileTimer: ReturnType<typeof setTimeout> | undefined;
	let listTimer: ReturnType<typeof setTimeout> | undefined;
	let listPending = false;

	// Bounded from the first event, not a quiet-period debounce: an agent
	// writing continuously still shows its progress.
	const refreshFiles = () => {
		if (fileTimer) return;
		fileTimer = setTimeout(() => {
			fileTimer = undefined;
			void queryClient.invalidateQueries({
				predicate: (query) => query.queryKey[0] === "session-artifact-file" && query.queryKey[2] === sessionId,
			});
		}, FILE_REFRESH_MS);
	};
	const refreshList = () => {
		if (listTimer) {
			listPending = true;
			return;
		}
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		listTimer = setTimeout(() => {
			listTimer = undefined;
			if (!listPending || disposed) return;
			listPending = false;
			refreshList();
		}, LIST_REFRESH_MS);
	};
	const refresh = () => {
		if (disposed) return;
		refreshFiles();
		refreshList();
	};
	const close = () => {
		if (retry) clearTimeout(retry);
		retry = undefined;
		source?.close();
		source = undefined;
		sourceBaseUrl = undefined;
	};
	const connect = () => {
		if (disposed || !hasTrustedApiBaseUrl() || typeof EventSource === "undefined") return;
		const baseUrl = getApiBaseUrl();
		if (sourceBaseUrl === baseUrl && (source || retry)) return;
		close();
		sourceBaseUrl = baseUrl;
		const next = new EventSource(`${eventStreamBaseUrl(baseUrl)}/api/v1/sessions/${encodeURIComponent(sessionId)}/artifact-events`);
		source = next;
		// Ready also covers a write made before the watch existed.
		next.addEventListener("ready", () => {
			failures = 0;
			refresh();
		});
		next.addEventListener("artifacts_changed", refresh);
		next.onerror = () => {
			// The browser retries a dropped stream itself; once it gives up (the
			// daemon refused it), rebuild with backoff.
			if (disposed || source !== next || next.readyState !== EVENTSOURCE_CLOSED) return;
			next.close();
			source = undefined;
			failures += 1;
			retry = setTimeout(() => {
				retry = undefined;
				connect();
			}, computeSseRetryDelayMs(failures));
		};
	};
	const stopBaseUrl = subscribeApiBaseUrl(connect);
	connect();
	return () => {
		disposed = true;
		stopBaseUrl();
		close();
		if (fileTimer) clearTimeout(fileTimer);
		if (listTimer) clearTimeout(listTimer);
	};
}
