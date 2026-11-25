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

type ChatImageSource = { sessionId: string; hostId?: string; version: number; baseUrl?: string; remoteHost: boolean; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"] };

const ChatImageSourceContext = createContext<ChatImageSource | undefined>(undefined);

/** hostId names the host a remote-host chat's session runs on, so requests about it reach that session's view. */
export function ChatImageSourceProvider({ sessionId, hostId, assetBaseUrl, remoteHost = false, artifacts, cloud, children }: { sessionId: string; hostId?: string; assetBaseUrl?: string; remoteHost?: boolean; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"]; children: ReactNode }) {
	// The blob route is no-store, so a mount-specific version makes rewritten
	// workspace images reload when the chat is reopened.
	const [version] = useState(() => Date.now());
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const baseUrl = remoteHost ? assetBaseUrl : assetBaseUrl ?? localBaseUrl;
	const value = useMemo(() => ({ sessionId, hostId, version, baseUrl, remoteHost, artifacts, cloud }), [sessionId, hostId, version, baseUrl, remoteHost, artifacts, cloud]);
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

/**
 * The open chat's session and host, when it lists the artifact file at path;
 * undefined when it does not, so nothing offers to open a file the session
 * cannot show.
 */
export function useChatArtifactLinks(path: string | undefined): { sessionId: string; hostId?: string } | undefined {
	const source = useContext(ChatImageSourceContext);
	const artifact = path === undefined ? undefined : source?.artifacts?.find((candidate) => candidate.path === path);
	if (!source || !artifact) return undefined;
	return { sessionId: source.sessionId, hostId: source.hostId };
}
