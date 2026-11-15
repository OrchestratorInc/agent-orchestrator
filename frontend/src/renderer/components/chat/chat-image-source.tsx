import { createContext, useContext, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";
import { getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { isAbsoluteMarkdownAssetSrc, resolveMarkdownImageSrc } from "../../lib/markdown-image-resolver";
import type { SessionArtifact, WorkspaceSession } from "../../types/workspace";
import { isInlineArtifactUrl } from "../../../shared/agent-page-url";

type ChatImageSource = { sessionId: string; version: number; baseUrl?: string; remoteHost: boolean; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"] };

const ChatImageSourceContext = createContext<ChatImageSource | undefined>(undefined);

export function ChatImageSourceProvider({ sessionId, assetBaseUrl, remoteHost = false, artifacts, cloud, children }: { sessionId: string; assetBaseUrl?: string; remoteHost?: boolean; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"]; children: ReactNode }) {
	const [version] = useState(() => Date.now());
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const baseUrl = remoteHost ? assetBaseUrl : assetBaseUrl ?? localBaseUrl;
	const value = useMemo(() => ({ sessionId, version, baseUrl, remoteHost, artifacts, cloud }), [sessionId, version, baseUrl, remoteHost, artifacts, cloud]);
	return <ChatImageSourceContext.Provider value={value}>{children}</ChatImageSourceContext.Provider>;
}

export function useChatImageSrc(src: string | undefined): string | undefined {
	const source = useContext(ChatImageSourceContext);
	if (!source) return src;
	if (source.remoteHost && !source.baseUrl && src && !isAbsoluteMarkdownAssetSrc(src)) return undefined;
	return resolveMarkdownImageSrc(source.sessionId, "", src, source.version, source.baseUrl);
}

export function useChatSessionId(): string | undefined {
	return useContext(ChatImageSourceContext)?.sessionId;
}

export function useChatArtifactFiles(): { sessionId: string; artifacts?: SessionArtifact[]; cloud?: WorkspaceSession["cloud"] } | undefined {
	const source = useContext(ChatImageSourceContext);
	return useMemo(
		() => (source ? { sessionId: source.sessionId, artifacts: source.artifacts, cloud: source.cloud } : undefined),
		[source],
	);
}

export function useChatRemoteHost(): boolean {
	return useContext(ChatImageSourceContext)?.remoteHost ?? false;
}

export function useChatArtifactLinks(path: string | undefined): { sessionId: string; previewUrl?: string; inlineUrl?: string } | undefined {
	const source = useContext(ChatImageSourceContext);
	const artifact = path === undefined ? undefined : source?.artifacts?.find((candidate) => candidate.path === path);
	if (!source || !artifact) return undefined;
	const inlineUrl = artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	return { sessionId: source.sessionId, previewUrl: artifact.previewUrl, inlineUrl };
}
