import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { sharedArtifactLinkId } from "../../../../packages/shared/artifacts/shared-artifact-link";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudGate } from "../hooks/useCloudGate";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import type { SharedArtifactLink } from "../lib/artifacts";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { useUiStore } from "../stores/ui-store";

/**
 * How long checking a link may take before it opens in its own window instead.
 * One small request; past this the control plane is struggling, and a waiting
 * check would hold every later link, which open one at a time.
 */
const SHARED_ARTIFACT_OPEN_TIMEOUT_MS = 15_000;

/**
 * Opens a published artifact sent from its web page's "Open on orchestrator"
 * (ao-app://shared-artifact) in the Artifacts page's preview, once the control
 * plane says it opens for this account. Opening it lists nothing: a link is
 * listed as shared with this person only because its owner shared it with
 * their email or a team of theirs. Signed out of Cloud, or when it does not
 * open for this account, it opens in its own isolated window instead, where
 * the viewer asks for sign-in. A link that arrives while the sign-in is still
 * being read waits for it.
 */
export function SharedArtifactLinkHost() {
	const { client, ready, userId } = useCloudCp();
	// The account signed in now, read when a check finishes.
	const currentUserId = useRef(userId);
	useEffect(() => {
		currentUserId.current = userId;
	}, [userId]);
	const { loading: gateLoading } = useCloudGate();
	const { status } = useCloudSession();
	const navigate = useNavigate();
	const [pending, setPending] = useState<string[]>([]);
	// One link is checked at a time, so they open in the order they came.
	const [checking, setChecking] = useState(false);
	// A checked link waits while Settings is open: Settings covers the page, and
	// closing it for the person could lose a project's unsaved changes.
	const settingsOpen = useUiStore((state) => state.settingsModal !== null);
	const [waiting, setWaiting] = useState<{ artifact: SharedArtifactLink; userId: string } | null>(null);

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
		if (!settled || checking || pending.length === 0) return;
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
		const checkedFor = userId;
		setChecking(true);
		void client.viewArtifactShare(linkId, { signal: AbortSignal.timeout(SHARED_ARTIFACT_OPEN_TIMEOUT_MS) }).then((view) => {
			// Another account signed in meanwhile: this one's link is not shown to it.
			if (currentUserId.current !== checkedFor) return;
			// Only the link ID reaches the control plane, so open it here only when
			// the link came from the viewer the control plane names for it; a link
			// on any other origin opens its own page in the isolated window.
			if (!sameOrigin(view.viewerUrl, viewerUrl)) {
				fallback();
				return;
			}
			const artifact: SharedArtifactLink = {
				linkId: view.linkId,
				name: view.name,
				contentType: view.contentType,
				updatedAt: view.updatedAt,
				access: view.access ?? "anyone",
				...(view.viewerUrl ? { viewerUrl: view.viewerUrl } : {}),
			};
			setWaiting({ artifact, userId: checkedFor });
		}, fallback).finally(() => setChecking(false));
	}, [settled, checking, pending, ready, client, userId]);

	// Shown on the Artifacts page once nothing covers it.
	useEffect(() => {
		if (!waiting || settingsOpen) return;
		setWaiting(null);
		if (currentUserId.current !== waiting.userId) return;
		useArtifactsViewStore.getState().requestOpenSharedArtifact(waiting.artifact);
		void navigate({ to: "/artifacts" });
	}, [waiting, settingsOpen, navigate]);

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
