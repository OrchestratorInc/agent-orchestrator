import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link2 } from "lucide-react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudLocalAuth } from "../hooks/useCloudLocalAuth";
import { ArtifactShareAuthError, ArtifactShareLimitError, ArtifactShareUnavailableError, publishArtifactShare } from "../lib/artifact-share";
import { aoBridge } from "../lib/bridge";
import { isCloudSignInConfigured, useCloudSession } from "../lib/cloud-session";
import { IS_DEV } from "../lib/is-dev";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useUiStore } from "../stores/ui-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";

/** Share links are development-only until the control plane offers them in hosted environments. */
export function artifactSharingAvailable(session: WorkspaceSession, hostId?: string): boolean {
	return IS_DEV && isCloudSignInConfigured() && !hostId && !session.cloud;
}

export type ArtifactShareState = "idle" | "signingIn" | "sharing";

/**
 * Shares one local artifact as a public link. A signed-out user is sent to
 * cloud sign-in first, and the share continues once they are signed in. On
 * success the link is copied and announced; at the link limit the upgrade
 * dialog opens.
 */
export function useArtifactShare(session: WorkspaceSession, artifact: SessionArtifact) {
	const { t } = useTranslation();
	const { baseUrl } = useCloudCp();
	const { status, signIn } = useCloudSession();
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const [state, setState] = useState<ArtifactShareState>("idle");
	const [error, setError] = useState("");
	const [limitOpen, setLimitOpen] = useState(false);
	const inFlight = useRef(false);

	const startSignIn = useCallback(
		() => (localAuthAvailable ? openLocalSignIn() : signIn()),
		[localAuthAvailable, openLocalSignIn, signIn],
	);

	const publish = useCallback(async () => {
		if (inFlight.current) return;
		inFlight.current = true;
		setState("sharing");
		setError("");
		try {
			const link = await publishArtifactShare({
				baseUrl,
				sessionId: session.id,
				path: artifact.path,
				name: artifact.name,
				rawUrl: artifact.rawUrl,
				project: session.workspaceName,
				feature: session.title,
			}, t("artifacts.share.error"));
			await aoBridge.clipboard.writeText(link.url);
			useUiStore.getState().showGlobalToast(
				t(link.replaced ? "artifacts.share.updated" : "artifacts.share.copied"),
				link.url,
				{ tone: "info", dedupeKey: `artifact-share:${link.id}` },
			);
			setState("idle");
		} catch (failure) {
			if (failure instanceof ArtifactShareAuthError) {
				setState("signingIn");
				startSignIn();
				return;
			}
			setState("idle");
			if (failure instanceof ArtifactShareLimitError) {
				setLimitOpen(true);
				return;
			}
			if (failure instanceof ArtifactShareUnavailableError) {
				setError(t("artifacts.share.unavailable"));
				return;
			}
			setError(failure instanceof Error ? failure.message : String(failure));
		} finally {
			inFlight.current = false;
		}
	}, [artifact.name, artifact.path, artifact.rawUrl, baseUrl, session.id, session.title, session.workspaceName, startSignIn, t]);

	const share = () => {
		setError("");
		if (status === "authenticated") {
			void publish();
			return;
		}
		setState("signingIn");
		startSignIn();
	};

	// The share the user asked for continues once sign-in completes.
	useEffect(() => {
		if (state === "signingIn" && status === "authenticated" && baseUrl) void publish();
	}, [baseUrl, publish, state, status]);

	const openUpgrade = () => {
		setLimitOpen(false);
		useUiStore.getState().openGlobalSettings("billing");
	};

	const limitDialog = (
		<ConfirmDialog
			cancelLabel={t("artifacts.share.notNow")}
			confirmLabel={t("artifacts.share.upgrade")}
			description={t("artifacts.share.limitDescription")}
			onConfirm={openUpgrade}
			onOpenChange={setLimitOpen}
			open={limitOpen}
			title={t("artifacts.share.limitTitle")}
		/>
	);

	const clearError = useCallback(() => setError(""), []);
	return { share, state, error, clearError, limitDialog };
}

const SHARE_ERROR_CLEAR_MS = 5_000;

/** The artifact viewer header's Share button: publishes the open artifact and copies its link. */
export function ArtifactShareButton({ artifact, session }: { artifact: SessionArtifact; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const { share, state, error, clearError, limitDialog } = useArtifactShare(session, artifact);
	useEffect(() => {
		if (!error) return;
		const timer = setTimeout(clearError, SHARE_ERROR_CLEAR_MS);
		return () => clearTimeout(timer);
	}, [clearError, error]);
	const sharing = state === "sharing";
	return (
		<>
			{error ? (
				<span className="min-w-0 truncate px-1 text-caption text-destructive" role="alert" title={error}>
					{error}
				</span>
			) : null}
			<Button aria-busy={sharing} className="shrink-0 gap-1" disabled={sharing} onClick={share} size="sm" type="button" variant="secondary">
				<Link2 aria-hidden="true" className="size-icon-sm" />
				{t(sharing ? "artifacts.share.sharing" : "artifacts.share.action")}
			</Button>
			{limitDialog}
		</>
	);
}
