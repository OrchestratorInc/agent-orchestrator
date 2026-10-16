import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { cloudCpFetch } from "../lib/cloud-cp/renderer-client";
import { PanelMessage } from "./WorkspaceDiffView";

/**
 * A cloud session's HTML artifact, framed from the control plane. The frame
 * is sandboxed without allow-same-origin, so the page runs in an opaque
 * origin; the control plane's own Content-Security-Policy sandboxes it too
 * and blocks its network requests. A file whose stored object is gone shows
 * as unavailable.
 */
export function CloudArtifactFrame({ title, url }: { title: string; url: string }) {
	const { t } = useTranslation();
	const status = useQuery({
		queryKey: ["cloud-artifact-status", url],
		queryFn: async () => (await cloudCpFetch(url, { method: "HEAD" })).status,
		retry: false,
	});
	if (status.isPending) return <PanelMessage>{t("files.loading")}</PanelMessage>;
	if (status.data === 404) return <PanelMessage>{t("artifacts.unavailable")}</PanelMessage>;
	return (
		<iframe
			className="block h-full w-full border-0 bg-white"
			referrerPolicy="no-referrer"
			sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"
			src={url}
			title={title}
		/>
	);
}
