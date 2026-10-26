import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, Copy, Link2 } from "lucide-react";
import type { CloudAccount } from "../../shared/cloud-account";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudLocalAuth } from "../hooks/useCloudLocalAuth";
import {
	ArtifactShareAuthError,
	ArtifactShareLimitError,
	ArtifactShareUnavailableError,
	publishArtifactShare,
	type ArtifactShareLink,
} from "../lib/artifact-share";
import { aoBridge } from "../lib/bridge";
import { isCloudSignInConfigured, useCloudSession } from "../lib/cloud-session";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useUiStore } from "../stores/ui-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";

/**
 * Shows the artifact Share button. Off until the control plane with share
 * links is deployed to production. VITE_AO_ARTIFACT_SHARES=1 turns it on in a
 * build without this change, for testing against a local control plane.
 */
export const ARTIFACT_SHARING_ENABLED = false;

function artifactSharingFlagOn(): boolean {
	return ARTIFACT_SHARING_ENABLED || import.meta.env.VITE_AO_ARTIFACT_SHARES === "1";
}

/** Whether this session's artifacts can be shared: the flag is on and they are local files. */
export function artifactSharingAvailable(session: WorkspaceSession, hostId?: string): boolean {
	return artifactSharingFlagOn() && isCloudSignInConfigured() && !hostId && !session.cloud;
}

/**
 * What the share panel shows: the sign-in prompt, sign-in in progress, the
 * link being created, the link, the link limit, or a failure.
 */
export type ArtifactSharePhase = "signedOut" | "signingIn" | "creating" | "ready" | "limit" | "failed";

/** Identifies one signed-in cloud session; a new sign-in or account changes it. */
function cloudSessionKey(account: CloudAccount | null): string {
	return account ? `${account.user.id}|${account.storedAt}` : "";
}

const COPIED_MS = 2_000;

/**
 * Shares one local artifact as a public link. Opening the share panel creates
 * the link for a signed-in user. A signed-out user, or one whose session the
 * control plane refuses, is told to sign in first; the link is created once
 * sign-in brings a new session.
 */
export function useArtifactShare(session: WorkspaceSession, artifact: SessionArtifact) {
	const { t } = useTranslation();
	const { baseUrl } = useCloudCp();
	const { session: cloudSession, status, signIn } = useCloudSession();
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const localSignInOpen = useLocalSignInDialogStore((s) => s.open);
	const [open, setOpen] = useState(false);
	const [phase, setPhase] = useState<ArtifactSharePhase>("signedOut");
	const [link, setLink] = useState<ArtifactShareLink | null>(null);
	const [error, setError] = useState("");
	const [copied, setCopied] = useState(false);
	const inFlight = useRef(false);
	// The session that was current when the user chose to sign in.
	const signInFrom = useRef<string | null>(null);
	const currentSessionKey = cloudSessionKey(cloudSession);
	const sessionKeyRef = useRef(currentSessionKey);
	sessionKeyRef.current = currentSessionKey;

	const create = useCallback(async () => {
		if (inFlight.current) return;
		inFlight.current = true;
		setPhase("creating");
		setError("");
		setCopied(false);
		try {
			const created = await publishArtifactShare({
				baseUrl,
				userId: cloudSession?.user.id ?? "",
				sessionId: session.id,
				path: artifact.path,
				name: artifact.name,
				rawUrl: artifact.rawUrl,
				project: session.workspaceName,
				feature: session.title,
			}, t("artifacts.share.error"));
			setLink(created);
			setPhase("ready");
		} catch (failure) {
			if (failure instanceof ArtifactShareAuthError) {
				setPhase("signedOut");
			} else if (failure instanceof ArtifactShareLimitError) {
				setPhase("limit");
			} else {
				setError(failure instanceof ArtifactShareUnavailableError
					? t("artifacts.share.unavailable")
					: failure instanceof Error ? failure.message : String(failure));
				setPhase("failed");
			}
		} finally {
			inFlight.current = false;
		}
	}, [artifact.name, artifact.path, artifact.rawUrl, baseUrl, cloudSession?.user.id, session.id, session.title, session.workspaceName, t]);

	const onOpenChange = (next: boolean) => {
		setOpen(next);
		if (!next || phase === "signingIn" || phase === "creating") return;
		if (status === "authenticated") void create();
		else setPhase("signedOut");
	};

	const startSignIn = () => {
		signInFrom.current = sessionKeyRef.current;
		setPhase("signingIn");
		if (localAuthAvailable) {
			// The sign-in dialog takes focus; the panel reopens with the link.
			setOpen(false);
			openLocalSignIn();
		} else {
			signIn();
		}
	};

	// The link is created once sign-in brings a new session. The session that
	// sent the user to sign in never retries on its own, so a session the
	// control plane refuses cannot loop.
	useEffect(() => {
		if (signInFrom.current === null || status !== "authenticated" || !baseUrl) return;
		if (currentSessionKey === signInFrom.current) return;
		signInFrom.current = null;
		setOpen(true);
		void create();
	}, [baseUrl, create, currentSessionKey, status]);

	// Closing the local sign-in dialog without signing in returns to the prompt.
	const localSignInWasOpen = useRef(false);
	useEffect(() => {
		const closed = localSignInWasOpen.current && !localSignInOpen;
		localSignInWasOpen.current = localSignInOpen;
		if (!closed || signInFrom.current === null || sessionKeyRef.current !== signInFrom.current) return;
		signInFrom.current = null;
		setPhase("signedOut");
		setOpen(true);
	}, [localSignInOpen]);

	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), COPIED_MS);
		return () => clearTimeout(timer);
	}, [copied]);

	const copy = async () => {
		if (!link) return;
		try {
			await aoBridge.clipboard.writeText(link.url);
			setCopied(true);
		} catch (failure) {
			setError(failure instanceof Error ? failure.message : String(failure));
			setPhase("failed");
		}
	};

	const openUpgrade = () => {
		setOpen(false);
		useUiStore.getState().openGlobalSettings("billing");
	};

	return { open, onOpenChange, phase, link, error, copied, copy, retry: () => void create(), startSignIn, openUpgrade };
}

/** The artifact viewer header's Share button and its share panel. */
export function ArtifactShareButton({ artifact, session }: { artifact: SessionArtifact; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const share = useArtifactShare(session, artifact);
	const title = t("artifacts.share.title", { name: artifact.name });
	return (
		<Popover onOpenChange={share.onOpenChange} open={share.open}>
			<PopoverTrigger asChild>
				<Button className="shrink-0 gap-1" size="sm" type="button" variant="secondary">
					<Link2 aria-hidden="true" className="size-icon-sm" />
					{t("artifacts.share.action")}
				</Button>
			</PopoverTrigger>
			<PopoverContent align="end" aria-label={title} className="flex w-80 flex-col gap-3 p-3" role="dialog">
				<p className="truncate text-sm font-medium text-foreground" title={artifact.name}>{title}</p>
				<ArtifactSharePanelBody share={share} />
			</PopoverContent>
		</Popover>
	);
}

function ArtifactSharePanelBody({ share }: { share: ReturnType<typeof useArtifactShare> }) {
	const { t } = useTranslation();
	switch (share.phase) {
		case "signedOut":
			return (
				<>
					<p className="text-control leading-5 text-muted-foreground">{t("artifacts.share.signInDescription")}</p>
					<Button className="self-end" onClick={share.startSignIn} size="sm" type="button" variant="primary">
						{t("artifacts.share.signIn")}
					</Button>
				</>
			);
		case "signingIn":
			return <p className="text-control leading-5 text-muted-foreground" role="status">{t("artifacts.share.signingIn")}</p>;
		case "creating":
			return <p className="text-control leading-5 text-muted-foreground" role="status">{t("artifacts.share.creating")}</p>;
		case "ready":
			return share.link ? (
				<>
					<div className="flex items-center gap-2">
						<Input
							aria-label={t("artifacts.share.linkLabel")}
							className="h-control-md min-w-0 flex-1 font-mono text-xs"
							onFocus={(event) => event.currentTarget.select()}
							readOnly
							value={share.link.url}
						/>
						<Button className="shrink-0 gap-1" onClick={() => void share.copy()} size="sm" type="button" variant="primary">
							{share.copied ? <Check aria-hidden="true" className="size-icon-sm" /> : <Copy aria-hidden="true" className="size-icon-sm" />}
							{t(share.copied ? "artifacts.share.copiedButton" : "artifacts.share.copy")}
						</Button>
					</div>
					<p className="text-caption leading-4 text-muted-foreground">
						{t(share.link.replaced ? "artifacts.share.updatedHint" : "artifacts.share.publicHint")}
					</p>
				</>
			) : null;
		case "limit":
			return (
				<>
					<p className="text-control leading-5 text-muted-foreground">{t("artifacts.share.limitDescription")}</p>
					<Button className="self-end" onClick={share.openUpgrade} size="sm" type="button" variant="primary">
						{t("artifacts.share.upgrade")}
					</Button>
				</>
			);
		case "failed":
			return (
				<>
					<p className="text-control leading-5 text-error" role="alert">{share.error}</p>
					<Button className="self-end" onClick={share.retry} size="sm" type="button" variant="secondary">
						{t("artifacts.share.retry")}
					</Button>
				</>
			);
	}
}
