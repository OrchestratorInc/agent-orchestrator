import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useGitHubAuthRequirement, useGitHubDeviceLogin } from "../hooks/useSystemRequirementsGate";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, settingsDialogContentClass, settingsDialogFooterClass } from "./ui/dialog";

/** Blocks the app until AO has a GitHub credential. Sign-in uses GitHub's
 * device flow through the daemon. A failed probe fails open, like the startup
 * requirements gate, so a daemon hiccup never locks the user out. Same frame
 * as ConfirmDialog, without a close control because it cannot be dismissed. */
export function GitHubSignInGate({ enabled }: { enabled: boolean }) {
	const { t } = useTranslation();
	const auth = useGitHubAuthRequirement(enabled);
	const device = useGitHubDeviceLogin();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const login = device.login;
	const pending = login?.state === "pending";
	const sawPendingRef = useRef(false);
	const refetchAuth = auth.refetch;

	useEffect(() => {
		if (pending) {
			sawPendingRef.current = true;
			return;
		}
		if (login?.state !== "succeeded" || !sawPendingRef.current) return;
		sawPendingRef.current = false;
		showGlobalToast(t("startup.githubConnected"));
		void refetchAuth();
	}, [login?.state, pending, refetchAuth, showGlobalToast, t]);

	if (!enabled || auth.data?.satisfied !== false) return null;

	const openVerification = async () => {
		if (!login?.userCode || !login.verificationUri) return;
		await aoBridge.clipboard.writeText(login.userCode);
		void aoBridge.app.openExternal(login.verificationUri);
	};
	const error = device.start.isError ? device.start.error.message : login?.state === "failed" ? login.error : undefined;
	const actionClass = "h-8 rounded-[10px] px-4 text-sm";

	return (
		<Dialog open onOpenChange={() => {}}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(440px,calc(100vw-24px))]")}
				onEscapeKeyDown={(event) => event.preventDefault()}
				onPointerDownOutside={(event) => event.preventDefault()}
				data-testid="github-sign-in-gate"
			>
				<div className="flex flex-col gap-1 p-5">
					<DialogTitle className="settings-dialog-title text-base">{t("startup.githubSetupTitle")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">{t("startup.githubSetupSignedOut")}</DialogDescription>
				</div>

				{pending ? (
					<div className="flex flex-col gap-1 px-5 pb-3">
						<p className="text-caption text-settings-muted">{t("startup.githubDeviceCodeHint")}</p>
						<p className="select-all font-mono text-xl font-semibold tracking-[0.2em] text-settings-title">{login.userCode}</p>
						<p className="text-caption text-settings-muted">{t("startup.githubDeviceWaiting")}</p>
					</div>
				) : null}
				{error ? (
					<p role="alert" className="px-5 pb-3 text-caption leading-4 text-error">
						{error}
					</p>
				) : null}

				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					{pending ? (
						<>
							<Button type="button" variant="footer" className={actionClass} disabled={device.cancel.isPending} onClick={() => device.cancel.mutate()}>
								{t("startup.githubLoginCancel")}
							</Button>
							<Button type="button" variant="footer-primary" className={actionClass} onClick={() => void openVerification()}>
								{t("startup.githubDeviceOpen")}
							</Button>
						</>
					) : (
						<>
							<Button type="button" variant="footer" className={actionClass} onClick={() => void window.ao?.menu?.action("app.quit")}>
								{t("startup.quit")}
							</Button>
							<Button type="button" variant="footer-primary" className={actionClass} disabled={device.start.isPending} onClick={() => device.start.mutate()}>
								{device.start.isPending
									? t("startup.githubLoginStarting")
									: error
										? t("startup.githubLoginTryAgain")
										: t("startup.githubLogin")}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
