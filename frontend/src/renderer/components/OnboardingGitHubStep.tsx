import { Check, Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { useGitHubSetup } from "../hooks/useGitHubSetup";
import { aoBridge } from "../lib/bridge";
import { Button } from "./ui/button";
import { ManualCommand } from "./InstallDependencyDialog";
import { SetupList, SetupRow } from "./SetupList";
import { GitHubMarkIcon } from "./icons";

const COPIED_RESET_MS = 2_000;
const DEFAULT_VERIFICATION_URL = "https://github.com/login/device";

/** Step: GitHub. One page handles both halves of the prerequisite, because
 *  installing the CLI and signing in are one intention. The checks run from
 *  the first step's mount, so this page opens already knowing the state. The
 *  sign-in is presented here (code, open GitHub, waiting) rather than in a
 *  terminal. */
export function OnboardingGitHubStep({ setup }: { setup: ReturnType<typeof useGitHubSetup> }) {
	const { t } = useTranslation();
	const installFailed = setup.job?.status === "failed" || setup.job?.status === "unsupported" || setup.job?.status === "interrupted";
	const installDetail = setup.installError ?? (installFailed ? setup.job?.error : undefined);
	// Linux cannot install gh for the user and says so with an unsupported job
	// plus the command that would work. Retrying only repeats that answer, so the
	// step has to hand the command over.
	const manualCommand = setup.job?.status === "unsupported" ? setup.job.command : undefined;

	if (!setup.gh) {
		return (
			<p className="px-1 text-caption text-muted-foreground" role="status">
				{t("onboarding.checkingAvailability")}
			</p>
		);
	}

	if (setup.authSatisfied) {
		return (
			<SetupList className="max-w-[420px] overflow-hidden rounded-xl">
				<SetupRow
					icon={<GitHubMarkIcon aria-hidden="true" />}
					label={t("startup.githubConnected")}
					description={t("onboarding.githubConnectedDetail")}
					trailing={<Check aria-hidden="true" className="size-3.5 text-status-ready" />}
					static
				/>
			</SetupList>
		);
	}

	const { login } = setup;
	const awaitingApproval = login.state === "awaiting_approval" && Boolean(login.userCode);
	const signInFailed = login.state === "failed";

	return (
		<div className="flex w-full max-w-[420px] flex-col text-left">
			{awaitingApproval ? (
				<DeviceCodePanel code={login.userCode ?? ""} url={login.verificationUrl ?? DEFAULT_VERIFICATION_URL} onCancel={setup.cancelSignIn} />
			) : (
				<SetupList>
					{setup.cliMissing ? (
						<SetupRow
							icon={<GitHubMarkIcon aria-hidden="true" />}
							label={installFailed ? t("onboarding.tryAgain") : t("startup.installGh")}
							description={t("onboarding.githubInstallDetail")}
							variant="action"
							disabled={setup.installing}
							onClick={() => void setup.install()}
							trailing={setup.installing ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" /> : undefined}
						/>
					) : (
						<SetupRow
							icon={<GitHubMarkIcon aria-hidden="true" />}
							label={signInFailed ? t("startup.githubLoginTryAgain") : setup.signInActive ? t("onboarding.githubGettingCode") : t("startup.githubLogin")}
							description={t("onboarding.githubSignInDetail")}
							variant="action"
							disabled={setup.signInActive}
							onClick={setup.signIn}
							trailing={setup.signInActive ? <Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" /> : undefined}
						/>
					)}
				</SetupList>
			)}
			{installDetail ? (
				<p className="px-4 text-caption leading-snug text-warning" role="status">
					{installDetail}
				</p>
			) : null}
			{manualCommand ? <ManualCommand command={manualCommand} t={t} /> : null}
			{signInFailed ? (
				<p className="px-4 pt-2 text-caption leading-snug text-destructive" role="alert">
					{login.error || t("onboarding.githubSignInFailed")}
				</p>
			) : null}
			{setup.signInError ? (
				<p className="px-4 pt-2 text-caption leading-snug text-destructive" role="alert">
					{setup.signInError}
				</p>
			) : null}
		</div>
	);
}

function DeviceCodePanel({ code, url, onCancel }: { code: string; url: string; onCancel: () => void }) {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);
	const resetTimer = useRef<number | undefined>(undefined);
	useEffect(() => () => window.clearTimeout(resetTimer.current), []);

	const copy = () => {
		void aoBridge.clipboard.writeText(code).then(() => {
			setCopied(true);
			window.clearTimeout(resetTimer.current);
			resetTimer.current = window.setTimeout(() => setCopied(false), COPIED_RESET_MS);
		});
	};

	return (
		<section aria-label={t("onboarding.githubCodeLabel")} className="flex flex-col items-center gap-5 py-2 text-center">
			<p className="text-caption text-muted-foreground">{t("onboarding.githubSignInInstruction")}</p>
			<p className="font-mono text-[32px] font-medium leading-none tracking-[0.12em] text-foreground tabular-nums select-all" data-testid="github-device-code">
				{code}
			</p>
			<div className="flex items-center gap-2">
				<Button type="button" variant="secondary" onClick={copy}>
					{copied ? <Check aria-hidden="true" /> : null}
					<span aria-live="polite">{copied ? t("onboarding.githubCodeCopied") : t("onboarding.githubCopyCode")}</span>
				</Button>
				<Button type="button" onClick={() => void aoBridge.app.openExternal(url)}>
					{t("onboarding.githubOpenGitHub")}
				</Button>
			</div>
			<div className="flex items-center gap-3 text-caption text-muted-foreground" role="status">
				<Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" />
				<span>{t("onboarding.githubWaiting")}</span>
				<button
					type="button"
					onClick={onCancel}
					className="rounded-md px-1.5 py-0.5 text-foreground hover:bg-foreground/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				>
					{t("confirm.cancel")}
				</button>
			</div>
		</section>
	);
}
