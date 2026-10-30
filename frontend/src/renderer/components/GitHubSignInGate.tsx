import { LoaderCircle } from "lucide-react";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useGitHubAuthRequirement, useGitHubDeviceLogin } from "../hooks/useSystemRequirementsGate";
import { aoBridge } from "../lib/bridge";
import { centeredOnboardingDialogClass, onboardingFieldErrorClass, onboardingFieldHintClass } from "../lib/onboarding-ui";
import { useUiStore } from "../stores/ui-store";
import { GitHubIcon } from "./GitHubIcon";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";

/** Onboarding step that blocks the app until AO has a GitHub credential.
 * Sign-in uses GitHub's device flow through the daemon. A failed probe fails
 * open, like the startup requirements gate, so a daemon hiccup never locks the
 * user out. It has no close control because it cannot be dismissed. */
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
	const primaryClass = "mt-6 h-9 w-full gap-2 rounded-[10px] text-sm";
	const quietClass = "mt-2 h-8 text-[13px] font-normal text-muted-foreground";

	return (
		<Dialog open onOpenChange={() => {}}>
			<DialogContent
				showCloseButton={false}
				className={cn(centeredOnboardingDialogClass, "w-[min(420px,calc(100vw-24px))]")}
				onEscapeKeyDown={(event) => event.preventDefault()}
				onPointerDownOutside={(event) => event.preventDefault()}
				data-testid="github-sign-in-gate"
			>
				<div className="flex flex-col items-center px-6 pb-5 pt-8 text-center">
					<span className="grid size-14 place-items-center rounded-2xl bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-title)]">
						<GitHubIcon className="size-7" />
					</span>
					<DialogTitle className="mt-4 text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
						{t("startup.githubSetupTitle")}
					</DialogTitle>
					<DialogDescription className="mt-1.5 text-pretty text-[13px] leading-5 text-muted-foreground">
						{t("startup.githubSetupSignedOut")}
					</DialogDescription>

					{pending ? (
						<>
							<div className="mt-5 w-full rounded-lg bg-[var(--color-bg-import-chip)] px-4 py-3">
								<p className="text-[12px] text-muted-foreground">{t("startup.githubDeviceCodeHint")}</p>
								<p className="mt-1 select-all font-mono text-[22px] font-semibold tracking-[0.2em] text-[var(--color-text-import-title)]">
									{login.userCode}
								</p>
							</div>
							<Button type="button" variant="footer-primary" className={primaryClass} onClick={() => void openVerification()}>
								<GitHubIcon className="size-4" />
								{t("startup.githubDeviceOpen")}
							</Button>
							<p className={cn(onboardingFieldHintClass, "mt-3 flex items-center gap-1.5")}>
								<LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
								{t("startup.githubDeviceWaiting")}
							</p>
							<Button type="button" variant="ghost" className={quietClass} disabled={device.cancel.isPending} onClick={() => device.cancel.mutate()}>
								{t("startup.githubLoginCancel")}
							</Button>
						</>
					) : (
						<>
							<Button type="button" variant="footer-primary" className={primaryClass} disabled={device.start.isPending} onClick={() => device.start.mutate()}>
								<GitHubIcon className="size-4" />
								{device.start.isPending
									? t("startup.githubLoginStarting")
									: error
										? t("startup.githubLoginTryAgain")
										: t("startup.githubLogin")}
							</Button>
							{error ? (
								<p role="alert" className={cn(onboardingFieldErrorClass, "mt-3")}>
									{error}
								</p>
							) : (
								<p className={cn(onboardingFieldHintClass, "mt-3")}>{t("startup.githubSignInNote")}</p>
							)}
							<Button type="button" variant="ghost" className={quietClass} onClick={() => void window.ao?.menu?.action("app.quit")}>
								{t("startup.quit")}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
