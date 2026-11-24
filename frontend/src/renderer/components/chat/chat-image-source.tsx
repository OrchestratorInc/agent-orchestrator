/**
 * Resolves relative chat image references against the current session workspace.
 *
 * Absolute sources pass through unchanged. Relative sources are resolved through
 * the workspace blob route at render time, so stored transcripts remain portable
 * across daemon ports and sessions.
 */

import { createContext, useContext, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";
import { getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { isAbsoluteMarkdownAssetSrc, resolveMarkdownImageSrc } from "../../lib/markdown-image-resolver";
import type { SessionArtifact, WorkspaceSession } from "../../types/workspace";
import { isInlineArtifactUrl } from "../../../shared/agent-page-url";

type ChatImageSource = { sessionId: string; version: number; baseUrl?: string; remoteHost: boolean; hostId?: string; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"] };

const ChatImageSourceContext = createContext<ChatImageSource | undefined>(undefined);

export function ChatImageSourceProvider({ sessionId, assetBaseUrl, remoteHost = false, hostId, artifacts, cloud, children }: { sessionId: string; assetBaseUrl?: string; remoteHost?: boolean; hostId?: string; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"]; children: ReactNode }) {
	// The blob route is no-store, so a mount-specific version makes rewritten
	// workspace images reload when the chat is reopened.
	const [version] = useState(() => Date.now());
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const baseUrl = remoteHost ? assetBaseUrl : assetBaseUrl ?? localBaseUrl;
	const value = useMemo(() => ({ sessionId, version, baseUrl, remoteHost, hostId, artifacts, cloud }), [sessionId, version, baseUrl, remoteHost, hostId, artifacts, cloud]);
	return <ChatImageSourceContext.Provider value={value}>{children}</ChatImageSourceContext.Provider>;
}

/** Resolve an image source for chat, leaving it unchanged outside a session. */
export function useChatImageSrc(src: string | undefined): string | undefined {
	const source = useContext(ChatImageSourceContext);
	if (!source) return src;
	if (source.remoteHost && !source.baseUrl && src && !isAbsoluteMarkdownAssetSrc(src)) return undefined;
	return resolveMarkdownImageSrc(source.sessionId, "", src, source.version, source.baseUrl);
}

/** The session of the open chat, or undefined outside one. */
export function useChatSessionId(): string | undefined {
	return useContext(ChatImageSourceContext)?.sessionId;
}

/** The open chat's session and the artifact files it lists, or undefined outside one. */
export function useChatArtifactFiles(): { sessionId: string; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"] } | undefined {
	const source = useContext(ChatImageSourceContext);
	return useMemo(
		() => (source ? { sessionId: source.sessionId, artifacts: source.artifacts, cloud: source.cloud } : undefined),
		[source],
	);
}

/** Whether the open chat runs on a remote host, whose session files the local daemon cannot serve. */
export function useChatRemoteHost(): boolean {
	return useContext(ChatImageSourceContext)?.remoteHost ?? false;
}

/** The open chat's remote host and session, with the host's proxy base while it is connected; undefined in a local chat. */
export function useChatRemoteSession(): { hostId?: string; sessionId: string; baseUrl?: string } | undefined {
	const source = useContext(ChatImageSourceContext);
	return source?.remoteHost ? { hostId: source.hostId, sessionId: source.sessionId, baseUrl: source.baseUrl } : undefined;
}

/**
 * The listed artifact a chat card names. A card keeps the path the page had
 * when the agent reported it; a page renamed since is found by that earlier
 * path, so an old card still shows it.
 */
function findChatArtifact(artifacts: readonly SessionArtifact[] | undefined, path: string): SessionArtifact | undefined {
	return artifacts?.find((candidate) => candidate.path === path)
		?? artifacts?.find((candidate) => candidate.previousPaths?.includes(path));
}

/**
 * What the session lists for one of its artifact files, by path: where it is
 * now, the Browser panel URL, and the URL that frames the page inline from its
 * own origin.
 */
export function useChatArtifactLinks(path: string | undefined): { sessionId: string; path: string; previewUrl?: string; inlineUrl?: string } | undefined {
	const source = useContext(ChatImageSourceContext);
	const artifact = path === undefined ? undefined : findChatArtifact(source?.artifacts, path);
	if (!source || !artifact) return undefined;
	const inlineUrl = artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	return { sessionId: source.sessionId, path: artifact.path, previewUrl: artifact.previewUrl, inlineUrl };
}
