import { Check, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useGitHubAuthRequirement, useGitHubDeviceLogin } from "../hooks/useSystemRequirementsGate";
import { aoBridge } from "../lib/bridge";
import { centeredOnboardingDialogClass, onboardingFieldErrorClass, onboardingFieldHintClass } from "../lib/onboarding-ui";
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
	const login = device.login;
	// Set once sign-in finishes here, so the success step stays up after the
	// credential check turns satisfied, until the user moves on.
	const [connected, setConnected] = useState<{ login: string; avatarUrl?: string } | null>(null);
	const [avatarLoaded, setAvatarLoaded] = useState(false);
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
		setConnected({ login: login?.login ?? "", avatarUrl: login?.avatarUrl });
		void refetchAuth();
	}, [login?.state, login?.login, login?.avatarUrl, pending, refetchAuth]);

	if (!enabled) return null;
	if (connected === null && auth.data?.satisfied !== false) return null;

	const openVerification = async () => {
		if (!login?.userCode || !login.verificationUri) return;
		await aoBridge.clipboard.writeText(login.userCode);
		void aoBridge.app.openExternal(login.verificationUri);
	};
	const error = device.start.isError ? device.start.error.message : login?.state === "failed" ? login.error : undefined;
	const buttonClass = "h-9 w-full gap-2 rounded-[10px] text-sm";

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
					<span className="relative grid size-14 place-items-center overflow-hidden rounded-2xl bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-title)]">
						<GitHubIcon
							className={cn("size-7 transition-[opacity,filter] duration-500 ease-out motion-reduce:transition-none", avatarLoaded && "opacity-0 blur-sm")}
						/>
						{connected?.avatarUrl ? (
							<img
								src={connected.avatarUrl}
								alt=""
								onLoad={() => setAvatarLoaded(true)}
								className={cn(
									"absolute inset-0 size-full object-cover transition-[opacity,filter,transform] duration-500 ease-out motion-reduce:transition-none",
									avatarLoaded ? "scale-100 opacity-100 blur-0" : "scale-110 opacity-0 blur-md",
								)}
							/>
						) : null}
					</span>
					<DialogTitle className="mt-4 text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
						{connected ? t("startup.githubConnectedTitle") : t("startup.githubSetupTitle")}
					</DialogTitle>
					<DialogDescription className="mt-1.5 text-pretty text-[13px] leading-5 text-muted-foreground">
						{connected?.login
							? t("startup.githubConnectedAs", { login: connected.login })
							: connected
								? t("startup.githubConnectedNoLogin")
								: t("startup.githubSetupSignedOut")}
					</DialogDescription>

					{connected ? (
						<>
							<ul className="mt-5 flex w-full flex-col gap-2 rounded-lg bg-[var(--color-bg-import-chip)] px-4 py-3 text-left text-[13px] leading-5 text-[var(--color-text-import-title)]">
								{(["startup.githubCanOpenPrs", "startup.githubCanReadIssues", "startup.githubCanTrackPrs"] as const).map((key) => (
									<li key={key} className="flex items-start gap-2">
										<Check className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />
										{t(key)}
									</li>
								))}
							</ul>
							<p className={cn(onboardingFieldHintClass, "mt-3")}>{t("startup.githubDisconnectHint")}</p>
							<Button type="button" variant="footer-primary" className={cn(buttonClass, "mt-5")} onClick={() => {
									setConnected(null);
									setAvatarLoaded(false);
								}}>
								{t("startup.githubGetStarted")}
							</Button>
						</>
					) : pending ? (
						<>
							<div className="mt-5 w-full rounded-lg bg-[var(--color-bg-import-chip)] px-4 py-3">
								<p className="text-[12px] text-muted-foreground">{t("startup.githubDeviceCodeHint")}</p>
								<p className="mt-1 select-all font-mono text-[22px] font-semibold tracking-[0.2em] text-[var(--color-text-import-title)]">
									{login.userCode}
								</p>
								<p className={cn(onboardingFieldHintClass, "mt-1 flex items-center justify-center gap-1.5")}>
									<LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />
									{t("startup.githubDeviceWaiting")}
								</p>
							</div>
							<div className="mt-5 flex w-full flex-col gap-2">
								<Button type="button" variant="footer-primary" className={buttonClass} onClick={() => void openVerification()}>
									<GitHubIcon className="size-4" />
									{t("startup.githubDeviceOpen")}
								</Button>
								<Button type="button" variant="footer" className={buttonClass} disabled={device.cancel.isPending} onClick={() => device.cancel.mutate()}>
									{t("startup.githubLoginCancel")}
								</Button>
							</div>
						</>
					) : (
						<>
							<div className="mt-6 flex w-full flex-col gap-2">
								<Button type="button" variant="footer-primary" className={buttonClass} disabled={device.start.isPending} onClick={() => device.start.mutate()}>
									<GitHubIcon className="size-4" />
									{device.start.isPending
										? t("startup.githubLoginStarting")
										: error
											? t("startup.githubLoginTryAgain")
											: t("startup.githubLogin")}
								</Button>
								<Button type="button" variant="footer" className={buttonClass} onClick={() => void window.ao?.menu?.action("app.quit")}>
									{t("startup.quit")}
								</Button>
							</div>
							{error ? (
								<p role="alert" className={cn(onboardingFieldErrorClass, "mt-3")}>
									{error}
								</p>
							) : null}
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
