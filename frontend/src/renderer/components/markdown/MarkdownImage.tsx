import { mediaCrossOrigin } from "../../lib/file-media";
import { snapshotBlobUrl } from "../../hooks/useSessionWorkspaceFiles";
import { getApiBaseUrl } from "../../lib/api-client";
import { useContext, useState } from "react";
import { MarkdownFileContext } from "./markdown-file-context";
import { resolveMarkdownAssetPath, resolveArtifactImageSrc, resolveMarkdownImageSrc } from "../../lib/markdown-image-resolver";
import { baseUrlForHost } from "../../lib/host-clients";

export function MarkdownImage({ src, alt }: { src?: string; alt?: string }) {
	const context = useContext(MarkdownFileContext);
	const [failedSrc, setFailedSrc] = useState<string | null>(null);
	const base = context?.hostId ? baseUrlForHost(context.hostId) : undefined;
	const resolvedSrc = context?.snapshotSource && src && !/^[a-z][a-z0-9+.-]*:/i.test(src) && !src.startsWith("//")
 ? (context.hostId && !base ? undefined : snapshotBlobUrl(base ?? getApiBaseUrl(),context.sessionId,resolveMarkdownAssetPath(src.startsWith("/") ? "" : context.filePath,src.replace(/^\/+/,"")),context.snapshotSource))
 : context?.artifactOrigin
		? resolveArtifactImageSrc(context.artifactOrigin, context.filePath, src)
		: context
		? context.hostId && !base ? undefined : resolveMarkdownImageSrc(context.sessionId, context.filePath, src, context.version, base)
		: src;
	if (!resolvedSrc || resolvedSrc === failedSrc) return <span className="text-muted-foreground">{alt ?? ""}</span>;
	return <img crossOrigin={mediaCrossOrigin(resolvedSrc)} src={resolvedSrc} alt={alt ?? ""} onError={() => setFailedSrc(resolvedSrc)} />;
}
