import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useGitHubAuthRequirement, useGitHubDeviceLogin } from "../hooks/useSystemRequirementsGate";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

/** Blocks the app until AO has a GitHub credential. Sign-in uses GitHub's
 * device flow through the daemon. A failed probe fails open, like the startup
 * requirements gate, so a daemon hiccup never locks the user out. */
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

	return (
		<Dialog open onOpenChange={() => {}}>
			<DialogContent
				showCloseButton={false}
				className={settingsDialogContentClass}
				onEscapeKeyDown={(event) => event.preventDefault()}
				onPointerDownOutside={(event) => event.preventDefault()}
				data-testid="github-sign-in-gate"
			>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("startup.githubSetupTitle")}</DialogTitle>
					<DialogDescription asChild>
						<div className="text-control leading-4 text-settings-muted">{t("startup.githubSetupSignedOut")}</div>
					</DialogDescription>
				</div>

				<div className={settingsDialogBodyClass}>
					{pending ? (
						<div className="flex flex-col gap-1">
							<p className="text-caption text-settings-muted">{t("startup.githubDeviceCodeHint")}</p>
							<p className="select-all font-mono text-[22px] font-semibold tracking-[0.2em] text-settings-title">{login.userCode}</p>
							<p className="text-caption text-settings-muted">{t("startup.githubDeviceWaiting")}</p>
						</div>
					) : null}
					{error ? (
						<p role="alert" className="text-caption leading-4 text-error">
							{error}
						</p>
					) : null}
				</div>

				<div className={settingsDialogFooterClass}>
					{pending ? (
						<button
							type="button"
							className="settings-footer-button"
							disabled={device.cancel.isPending}
							onClick={() => device.cancel.mutate()}
						>
							{t("startup.githubLoginCancel")}
						</button>
					) : (
						<button
							type="button"
							className="settings-footer-button"
							onClick={() => void window.ao?.menu?.action("app.quit")}
						>
							{t("startup.quit")}
						</button>
					)}
					{pending ? (
						<button
							type="button"
							className="settings-footer-button settings-footer-button-primary"
							onClick={() => void openVerification()}
						>
							{t("startup.githubDeviceOpen")}
						</button>
					) : (
						<button
							type="button"
							className="settings-footer-button settings-footer-button-primary"
							disabled={device.start.isPending}
							onClick={() => device.start.mutate()}
						>
							{device.start.isPending
								? t("startup.githubLoginStarting")
								: error
									? t("startup.githubLoginTryAgain")
									: t("startup.githubLogin")}
						</button>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
