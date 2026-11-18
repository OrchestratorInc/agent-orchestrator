import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { sharedArtifactLinkId } from "../../../../packages/shared/artifacts/shared-artifact-link";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudGate } from "../hooks/useCloudGate";
import { sharedArtifactsQueryKey } from "../hooks/useSharedArtifacts";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";

/**
 * Opens a published artifact sent from its web page's "Open on orchestrator"
 * (ao-app://shared-artifact) on the Artifacts page: it is saved to the links
 * shared with this person, so it stays listed, and shown in the page's
 * preview. Signed out of Cloud, or when it does not open for this account, it
 * opens in its own isolated window instead, where the viewer asks for sign-in.
 * A link that arrives while the sign-in is still being read waits for it.
 */
export function SharedArtifactLinkHost() {
	const { client, ready } = useCloudCp();
	const { loading: gateLoading } = useCloudGate();
	const { status } = useCloudSession();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [pending, setPending] = useState<string[]>([]);

	useEffect(() => {
		const add = (viewerUrl: string | null) => {
			if (viewerUrl) setPending((current) => [...current, viewerUrl]);
		};
		// The main process keeps each link, in order, until it is taken, so ones
		// sent before this subscribed wait; a send only says to take them now.
		const take = async () => {
			for (let viewerUrl = await aoBridge.cloud.takePendingSharedArtifact(); viewerUrl; viewerUrl = await aoBridge.cloud.takePendingSharedArtifact()) {
				add(viewerUrl);
			}
		};
		const stop = aoBridge.cloud.onOpenSharedArtifact(() => void take());
		void take();
		return stop;
	}, []);

	const settled = !gateLoading && status !== "loading";
	useEffect(() => {
		if (!settled || pending.length === 0) return;
		const [viewerUrl, ...rest] = pending;
		setPending(rest);
		if (!viewerUrl) return;
		const linkId = sharedArtifactLinkId(viewerUrl);
		if (!linkId) return;
		const fallback = () => void aoBridge.cloud.openSharedArtifactWindow(viewerUrl);
		if (!ready) {
			fallback();
			return;
		}
		void client.saveSharedArtifact(linkId).then(({ artifact }) => {
			// Only the link ID reaches the control plane, so open it here only when
			// the link came from the viewer the control plane names for it; a link
			// on any other origin opens its own page in the isolated window.
			if (!sameOrigin(artifact.viewerUrl, viewerUrl)) {
				fallback();
				return;
			}
			void queryClient.invalidateQueries({ queryKey: sharedArtifactsQueryKey });
			useArtifactsViewStore.getState().requestOpenSharedArtifact(artifact);
			void navigate({ to: "/artifacts" });
		}, fallback);
	}, [settled, pending, ready, client, navigate, queryClient]);

	return null;
}

/** Whether two URLs share an origin; false when either is missing or invalid. */
function sameOrigin(left: string | undefined, right: string): boolean {
	if (!left) return false;
	try {
		return new URL(left).origin === new URL(right).origin;
	} catch {
		return false;
	}
}
