import { useEffect, useRef, type ReactNode, type Ref } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { CloudCpError } from "../lib/cloud-cp";
import { fileMediaKind } from "../lib/file-media";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";

/**
 * A link someone else published, framed from its file host. The URL comes
 * from viewArtifactShare with this person's sign-in, so a private link's
 * carries a short-lived view token and opens only while it is still shared
 * with them. The frame is sandboxed without allow-same-origin, and the file
 * host's own Content-Security-Policy sandboxes the page and blocks its network
 * requests. A token outlives the frame's load, so an open page keeps showing;
 * reopening takes a fresh one.
 *
 * A video plays in a video element, as on the link's web page: its file host
 * redirects to a signed URL on the video host, and a frame would load that as
 * a document, which a video stored without its type turns into a refused
 * download.
 */
/**
 * Where a media file's URL came from: its view token works for a while
 * (shareViewTokenTTL, 10 minutes), and a video asks for more bytes on every
 * seek, so it needs a fresh URL once the token is near its end.
 */
export type SharedMediaSource = {
	/** When the URL was fetched (ms since the epoch). */
	fetchedAt: number;
	/** Fetches a fresh URL (a new contentUrl follows). */
	refresh: () => void;
};

export function SharedArtifactFrame({ frameRef, linkId, onShown, renderMedia, title }: {
	/** The page's frame, for the comments bridge; a video has none. */
	frameRef?: Ref<HTMLIFrameElement>;
	/** Called once there is something to see: the page loaded, or a message in its place. */
	onShown?: () => void;
	linkId: string;
	/**
	 * Comments on an image or video: draws the file itself from its content
	 * URL (with its own overlay) instead of the frame or plain video. Call
	 * `shown` once there is something to see.
	 */
	renderMedia?: (contentUrl: string, shown: () => void, source: SharedMediaSource) => ReactNode;
	title: string;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl, userId } = useCloudCp();
	const view = useQuery({
		// Per account: the view URL carries this account's access.
		queryKey: ["shared-artifact-view", baseUrl, userId, linkId],
		enabled: ready,
		staleTime: 5 * 60_000,
		gcTime: 0,
		retry: (count, error) => !(error instanceof CloudCpError && error.status === 404) && count < 1,
		queryFn: ({ signal }) => client.viewArtifactShare(linkId, { signal }),
	});
	const onShownRef = useRef(onShown);
	onShownRef.current = onShown;
	// A message (sign in, gone, error) is something to see; a page is once it loads.
	const showsMessage = !ready || view.isError;
	useEffect(() => {
		if (showsMessage) onShownRef.current?.();
	}, [showsMessage]);
	if (!ready) return <PanelMessage>{t("artifacts.sharedSignIn")}</PanelMessage>;
	if (view.error instanceof CloudCpError && view.error.status === 404) return <PanelMessage>{t("artifacts.sharedUnavailable")}</PanelMessage>;
	if (view.isError) return <PanelMessage action={<RetryButton onClick={() => { void view.refetch(); }} />}>{t("artifacts.accessError")}</PanelMessage>;
	if (!view.data) return <PanelMessage>{t("files.loading")}</PanelMessage>;
	if (renderMedia) {
		return renderMedia(view.data.contentUrl, () => onShownRef.current?.(), {
			fetchedAt: view.dataUpdatedAt,
			refresh: () => { void view.refetch(); },
		});
	}
	if (isSharedVideo(view.data.contentType, view.data.name)) {
		return (
			<div className="flex h-full w-full items-center justify-center bg-background">
				<video
					aria-label={title}
					className="max-h-full max-w-full"
					controls
					controlsList="nodownload"
					key={view.data.contentUrl}
					onLoadedMetadata={() => onShownRef.current?.()}
					playsInline
					preload="metadata"
					src={view.data.contentUrl}
				/>
			</div>
		);
	}
	return (
		<iframe
			className="h-full w-full border-0 bg-background"
			key={view.data.contentUrl}
			onLoad={() => onShownRef.current?.()}
			ref={frameRef}
			referrerPolicy="no-referrer"
			sandbox="allow-scripts allow-forms"
			src={view.data.contentUrl}
			title={title}
		/>
	);
}

/** Whether a shared link is a video: by its type, or by its name when stored without one. */
function isSharedVideo(contentType: string | undefined, name: string): boolean {
	const type = contentType?.split(";")[0]?.trim().toLowerCase() ?? "";
	if (type.startsWith("video/")) return true;
	return (type === "" || type === "application/octet-stream" || type === "binary/octet-stream") && fileMediaKind(name) === "video";
}
