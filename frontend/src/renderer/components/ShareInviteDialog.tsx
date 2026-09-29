import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudLocalAuth } from "../hooks/useCloudLocalAuth";
import { SHARED_WITH_ME_WORKSPACE_ID, sharedCloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import {
	centeredOnboardingDialogClass,
	onboardingAlertErrorClass,
	onboardingFooterActionsEndClass,
} from "../lib/onboarding-ui";
import { cn } from "../lib/utils";
import { useLocalSignInDialogStore } from "../stores/local-signin-dialog-store";
import { useShareDialogStore } from "../stores/share-dialog-store";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";

// Recipient half of session sharing. An ao-app://share deep link is
// validated in main and queued; this dialog pulls it, asks the user to sign in
// if needed, shows who is inviting them to what, and only redeems the link on
// an explicit Accept. Decline (or closing) grants nothing.
export function ShareInviteDialog() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const { client, ready } = useCloudCp();
	const { session: account, status, signIn } = useCloudSession();
	const localAuth = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);
	const invite = useShareDialogStore((s) => s.invite);
	const setInvite = useShareDialogStore((s) => s.setInvite);
	const clearInvite = useShareDialogStore((s) => s.clearInvite);
	const [accepting, setAccepting] = useState(false);
	const [acceptError, setAcceptError] = useState(false);

	useEffect(() => {
		const take = () => {
			void aoBridge.cloud.takePendingShareInvite().then((pending) => {
				if (pending) setInvite(pending);
			});
		};
		take();
		return aoBridge.cloud.onShareInvitePending(take);
	}, [setInvite]);

	useEffect(() => {
		setAccepting(false);
		setAcceptError(false);
	}, [invite]);

	const preview = useQuery({
		queryKey: ["cloud-share-invite", invite?.linkId, account?.user.id],
		enabled: invite !== null && ready,
		retry: false,
		staleTime: Infinity,
		queryFn: async () => {
			const response = await client.previewSessionShareDeepLink({
				orgId: invite!.orgId,
				linkId: invite!.linkId,
				token: invite!.secret,
			});
			return response.invite;
		},
	});

	const accept = async () => {
		if (!invite || accepting) return;
		setAccepting(true);
		setAcceptError(false);
		try {
			const { shared } = await client.redeemSessionShareDeepLink({
				orgId: invite.orgId,
				linkId: invite.linkId,
				token: invite.secret,
			});
			await queryClient.invalidateQueries({ queryKey: sharedCloudSessionsQueryKey });
			clearInvite();
			if (shared.sessionId) {
				void navigate({
					to: "/projects/$projectId/sessions/$sessionId",
					params: { projectId: SHARED_WITH_ME_WORKSPACE_ID, sessionId: shared.sessionId },
				});
			}
		} catch {
			setAcceptError(true);
			setAccepting(false);
		}
	};

	const invitation = preview.data;
	const minutes = invitation ? Math.max(1, Math.round((Date.parse(invitation.expiresAt) - Date.now()) / 60_000)) : 0;
	const signedOut = status === "unauthenticated";
	const denied = preview.isError || acceptError;

	return (
		<Dialog open={invite !== null} onOpenChange={(open) => !open && !accepting && clearInvite()}>
			<DialogContent className={centeredOnboardingDialogClass} showCloseButton={false}>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")} disabled={accepting}>
						<X className="size-icon-base" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className="px-4 pr-12 pt-3">
					<DialogTitle className="text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
						{t("share.inviteTitle")}
					</DialogTitle>
				</div>
				<DialogDescription className="px-4 pr-12 pt-1 pb-3 text-pretty text-[13px] leading-5 text-muted-foreground">
					{signedOut
						? t("share.inviteSignIn")
						: denied
							? ""
							: invitation
								? t("share.inviteBody", {
										inviter: invitation.inviterName || invitation.inviterEmail,
										session: invitation.sessionName || invitation.sessionId,
										project: invitation.projectName,
									})
								: t("share.inviteChecking")}
				</DialogDescription>

				<div className="flex flex-col gap-3 px-4">
					{denied ? (
						<p role="alert" className={onboardingAlertErrorClass}>
							{t("share.inviteDenied", { email: account?.user.email ?? "" })}
						</p>
					) : null}
					{invitation && !denied ? (
						<ul className="list-disc space-y-1 pl-5 text-[13px] leading-5 text-muted-foreground">
							<li>{invitation.role === "editor" ? t("share.inviteRoleInteract") : t("share.inviteRole")}</li>
							<li>{t("share.inviteExpiry", { minutes })}</li>
						</ul>
					) : null}
					{account && !signedOut ? (
						<p className="text-[12px] text-muted-foreground">{t("share.inviteSignedInAs", { email: account.user.email })}</p>
					) : null}
				</div>

				<div className={cn(onboardingFooterActionsEndClass, "px-4 pb-4")}>
					{signedOut ? (
						<Button type="button" variant="primary" onClick={() => (localAuth.available ? openLocalSignIn() : signIn())}>
							{t("share.signIn")}
						</Button>
					) : denied ? (
						<DialogClose asChild>
							<Button type="button" variant="outline">
								{t("common.close")}
							</Button>
						</DialogClose>
					) : (
						<>
							<DialogClose asChild>
								<Button type="button" variant="outline" disabled={accepting}>
									{t("share.decline")}
								</Button>
							</DialogClose>
							<Button type="button" variant="primary" disabled={!invitation || accepting} onClick={() => void accept()}>
								{accepting ? t("share.accepting") : t("share.accept")}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
