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
import type { SessionArtifact } from "../../types/workspace";
import { isInlineArtifactUrl } from "../../../shared/agent-page-url";

type ChatImageSource = { sessionId: string; version: number; baseUrl?: string; remoteHost: boolean; artifacts?: SessionArtifact[] };

const ChatImageSourceContext = createContext<ChatImageSource | undefined>(undefined);

export function ChatImageSourceProvider({ sessionId, assetBaseUrl, remoteHost = false, artifacts, children }: { sessionId: string; assetBaseUrl?: string; remoteHost?: boolean; artifacts?: SessionArtifact[]; children: ReactNode }) {
	// The blob route is no-store, so a mount-specific version makes rewritten
	// workspace images reload when the chat is reopened.
	const [version] = useState(() => Date.now());
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const baseUrl = remoteHost ? assetBaseUrl : assetBaseUrl ?? localBaseUrl;
	const value = useMemo(() => ({ sessionId, version, baseUrl, remoteHost, artifacts }), [sessionId, version, baseUrl, remoteHost, artifacts]);
	return <ChatImageSourceContext.Provider value={value}>{children}</ChatImageSourceContext.Provider>;
}

/** Resolve an image source for chat, leaving it unchanged outside a session. */
export function useChatImageSrc(src: string | undefined): string | undefined {
	const source = useContext(ChatImageSourceContext);
	if (!source) return src;
	if (source.remoteHost && !source.baseUrl && src && !isAbsoluteMarkdownAssetSrc(src)) return undefined;
	return resolveMarkdownImageSrc(source.sessionId, "", src, source.version, source.baseUrl);
}

/** Whether the open chat runs on a remote host, whose session files the local daemon cannot serve. */
export function useChatRemoteHost(): boolean {
	return useContext(ChatImageSourceContext)?.remoteHost ?? false;
}

/**
 * What the session lists for one of its artifact files, by path: the Browser
 * panel URL, and the URL that frames the page inline from its own origin.
 */
export function useChatArtifactLinks(path: string | undefined): { sessionId: string; previewUrl?: string; inlineUrl?: string } | undefined {
	const source = useContext(ChatImageSourceContext);
	const artifact = path === undefined ? undefined : source?.artifacts?.find((candidate) => candidate.path === path);
	if (!source || !artifact) return undefined;
	const inlineUrl = artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	return { sessionId: source.sessionId, previewUrl: artifact.previewUrl, inlineUrl };
}
