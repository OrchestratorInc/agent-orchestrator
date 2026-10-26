import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useHostConnection } from "../hooks/useHostConnection";
import { snapshotBlobUrl, type FilesSource } from "../hooks/useSessionWorkspaceFiles";
import { getApiBaseUrl } from "../lib/api-client";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { mediaCrossOrigin, type FileMediaKind } from "../lib/file-media";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";

export function FileMediaPreview({ hostId, kind, path, sessionId, source, version, side = "after" }: {
	side?: "before" | "after";
 hostId?: string;
	kind: FileMediaKind;
	path: string;
	sessionId: string;
	source: FilesSource;
	version: number | string;
}) {
	const { t } = useTranslation();
	const { baseUrl: remoteBaseUrl } = useHostConnection(hostId);
	const [failedSrc, setFailedSrc] = useState<string | null>(null);
	// The file version a fresh cloud link was already taken for.
	const [refreshedFor, setRefreshedFor] = useState<string | null>(null);
	const base = hostId ? remoteBaseUrl : getApiBaseUrl();
	// Use the streamed preview route: the image-diff blob route buffers whole
	// revisions and caps them at 16 MiB, which is too small for recordings.
	const rawUrl = source.kind === "snapshot" ? base === undefined ? undefined : snapshotBlobUrl(base,sessionId,path,source,side) : source.kind === "artifact" ? artifactContentUrl(sessionId, path, source.rawUrl) : base === undefined ? undefined
		: `${base}/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${path.split("/").map(encodeURIComponent).join("/")}?raw=true&v=${encodeURIComponent(version)}`;
	// A cloud file's URL already carries its version.
	const src = source.kind === "artifact" && rawUrl && !source.cloud ? `${rawUrl}${rawUrl.includes("?") ? "&" : "?"}v=${encodeURIComponent(version)}` : rawUrl;
	if (!src || failedSrc === src) {
		const retry = () => {
			setFailedSrc(null);
			setRefreshedFor(null);
		};
		return <PanelMessage action={src ? <RetryButton onClick={retry} /> : undefined}>{t("files.error.loadFile")}</PanelMessage>;
	}
	// A cloud file's link may have expired: take a fresh one, once per file
	// version, which changes src and loads again. A file that fails with a
	// fresh link too, such as a corrupt one, shows the failure.
	const refreshLink = source.kind === "artifact" && source.cloud ? source.onAccessRefused : undefined;
	const fileVersion = `${path}\n${version}`;
	const onError = () => {
		setFailedSrc(src);
		if (refreshLink && refreshedFor !== fileVersion) {
			setRefreshedFor(fileVersion);
			refreshLink();
		}
	};
	return (
		<div className="grid min-h-0 min-w-0 flex-1 grid-rows-[minmax(0,1fr)] place-items-center overflow-hidden p-3">
			{kind === "image" ? (
				<img crossOrigin={mediaCrossOrigin(src)} alt={path} aria-label={path} className="size-full min-h-0 min-w-0 object-contain" key={src} onError={onError} src={src} />
			) : kind === "video" ? (
				<video crossOrigin={mediaCrossOrigin(src)} aria-label={path} className="size-full min-h-0 min-w-0 object-contain" controls key={src} onError={onError} playsInline preload="metadata" src={src} />
			) : (
				<audio crossOrigin={mediaCrossOrigin(src)} aria-label={path} className="w-full max-w-lg" controls key={src} onError={onError} preload="metadata" src={src} />
			)}
		</div>
	);
}
