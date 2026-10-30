import { GitPullRequest, TerminalSquare, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TerminalSessionState } from "../hooks/useTerminalSession";
import { useCloseShellTerminal } from "../hooks/useShellTerminals";
import { useGitHubAuthRequirement, useGitHubAuthTerminal, useGitHubDeviceLogin, useStartGitHubAuthTerminal, useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { aoBridge } from "../lib/bridge";
import { useOnboardingStep } from "../lib/onboarding-telemetry";
import { useShellMaybe } from "../lib/shell-context";
import { useResolvedTheme, useUiStore } from "../stores/ui-store";
import { TerminalPane } from "./TerminalPane";
import { TopbarButton } from "./TopbarButton";

/** Onboarding advisory. GitHub is not required for local work, but surfacing
 * missing auth before task creation prevents a late PR-creation failure inside
 * an agent session. Sign-in uses GitHub's device flow through the daemon, so
 * the GitHub CLI is optional; users who already have it can sign in with it. */
export function GitHubOnboardingNotice() {
	const { t } = useTranslation();
	const gate = useSystemRequirementsGate();
	const device = useGitHubDeviceLogin();
	const startLogin = useStartGitHubAuthTerminal();
	const terminalQuery = useGitHubAuthTerminal();
	const terminal = terminalQuery.data;
	const [terminalStatus, setTerminalStatus] = useState<{ handleId: string | null; state: TerminalSessionState }>({
		handleId: null,
		state: "idle",
	});
	const terminalState = terminalStatus.handleId === terminal?.handleId ? terminalStatus.state : "idle";
	const loginRunning = Boolean(terminal && (terminalState === "connecting" || terminalState === "attached" || terminalState === "reattaching"));
	const loginEnded = Boolean(terminal && (terminalState === "exited" || terminalState === "error"));
	const authQuery = useGitHubAuthRequirement(loginRunning);
	const { mutate: closeTerminal } = useCloseShellTerminal();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	const requirements = gate.requirements ?? [];
	const terminalRef = useRef(terminal);
	const refetchAuthRef = useRef(authQuery.refetch);
	const exitCheckedTerminalRef = useRef<string | null>(null);
	const completedTerminalRef = useRef<string | null>(null);
	const sawDevicePendingRef = useRef(false);
	const [loginFocusRequested, setLoginFocusRequested] = useState(false);
	const [manualCheckPending, setManualCheckPending] = useState(false);
	const gh = requirements.find((requirement) => requirement.id === "gh");
	const auth = authQuery.data;
	const deviceLogin = device.login;
	const devicePending = deviceLogin?.state === "pending";

	terminalRef.current = terminal;
	refetchAuthRef.current = authQuery.refetch;
	const handleTerminalState = useCallback((state: TerminalSessionState) => {
		const active = terminalRef.current;
		setTerminalStatus({ handleId: active?.handleId ?? null, state });
		if (!active || (state !== "exited" && state !== "error")) return;
		if (exitCheckedTerminalRef.current === active.handleId) return;
		exitCheckedTerminalRef.current = active.handleId;
		void refetchAuthRef.current();
	}, []);
	useEffect(() => {
		if (!auth?.satisfied || !terminal) return;
		if (completedTerminalRef.current === terminal.handleId) return;
		completedTerminalRef.current = terminal.handleId;
		showGlobalToast(t("startup.githubConnected"));
		closeTerminal(terminal.handleId, { onSuccess: terminalQuery.clear });
	}, [auth?.satisfied, closeTerminal, showGlobalToast, t, terminal, terminalQuery.clear]);
	useEffect(() => {
		if (devicePending) {
			sawDevicePendingRef.current = true;
			return;
		}
		if (deviceLogin?.state !== "succeeded" || !sawDevicePendingRef.current) return;
		sawDevicePendingRef.current = false;
		showGlobalToast(t("startup.githubConnected"));
		void refetchAuthRef.current();
	}, [deviceLogin?.state, devicePending, showGlobalToast, t]);

	useOnboardingStep("github_auth", Boolean(auth && !auth.satisfied));
	if (!auth || auth.satisfied) return null;

	const openCliLogin = () => {
		setLoginFocusRequested(true);
		startLogin.mutate();
	};
	const retryCliLogin = () => {
		if (!terminal) return;
		setLoginFocusRequested(true);
		closeTerminal(terminal.handleId, {
			onSuccess: () => {
				terminalQuery.clear();
				startLogin.mutate();
			},
		});
	};
	const openDeviceVerification = async () => {
		if (!deviceLogin?.userCode || !deviceLogin.verificationUri) return;
		await aoBridge.clipboard.writeText(deviceLogin.userCode);
		void aoBridge.app.openExternal(deviceLogin.verificationUri);
	};

	const checkAgain = async () => {
		setManualCheckPending(true);
		try {
			await authQuery.refetch();
		} finally {
			setManualCheckPending(false);
		}
	};
	const closeLogin = () => {
		setLoginFocusRequested(false);
		if (!terminal) return;
		closeTerminal(terminal.handleId, { onSuccess: terminalQuery.clear });
	};
	// Unknown CLI readiness still offers the CLI; only a confirmed missing gh hides it.
	const cliAvailable = gh?.satisfied !== false;
	const deviceError = device.start.isError ? device.start.error.message : deviceLogin?.state === "failed" ? deviceLogin.error : undefined;
	return (
		<div className="flex w-full justify-center px-3">
			<div className="w-full max-w-[620px] rounded-welcome-panel border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-card)] px-4 py-4" role="status">
				<div className="flex items-start gap-3">
					<span className="grid size-9 shrink-0 place-items-center rounded-lg bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-muted)]">
						<GitPullRequest className="size-4" aria-hidden="true" />
					</span>
					<div className="min-w-0 flex-1">
						<p className="text-[14px] font-semibold text-[var(--color-text-import-title)]">{t("startup.githubSetupTitle")}</p>
						<p className="mt-0.5 text-[12px] leading-5 text-[var(--color-text-import-muted)]">{t("startup.githubSetupSignedOut")}</p>
						{devicePending ? (
							<div className="mt-3 rounded-lg border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-modal)] px-3 py-3" data-testid="github-device-login">
								<p className="text-[12px] text-[var(--color-text-import-muted)]">{t("startup.githubDeviceCodeHint")}</p>
								<p className="mt-1 select-all font-mono text-[20px] font-semibold tracking-[0.2em] text-[var(--color-text-import-title)]">{deviceLogin.userCode}</p>
								<div className="mt-2 flex flex-wrap items-center gap-2">
									<TopbarButton onClick={() => void openDeviceVerification()} variant="primary">
										{t("startup.githubDeviceOpen")}
									</TopbarButton>
									<TopbarButton disabled={device.cancel.isPending} onClick={() => device.cancel.mutate()} variant="accent">
										{t("startup.githubLoginCancel")}
									</TopbarButton>
								</div>
								<p className="mt-2 text-[11px] text-[var(--color-text-import-muted)]">{t("startup.githubDeviceWaiting")}</p>
							</div>
						) : (
							<div className="mt-2 flex flex-wrap items-center gap-2">
								<TopbarButton disabled={device.start.isPending} onClick={() => device.start.mutate()} variant="primary">
									{device.start.isPending
										? t("startup.githubLoginStarting")
										: deviceError
											? t("startup.githubLoginTryAgain")
											: t("startup.githubLogin")}
								</TopbarButton>
								{cliAvailable ? (
									<TopbarButton
										disabled={startLogin.isPending || Boolean(terminal && !loginEnded)}
										onClick={() => (loginEnded ? retryCliLogin() : openCliLogin())}
										variant="accent"
									>
										<TerminalSquare className="size-icon-sm" aria-hidden="true" />
										{t("startup.githubUseCli")}
									</TopbarButton>
								) : null}
								{loginRunning ? null : (
									<TopbarButton
										disabled={manualCheckPending}
										onClick={() => void checkAgain()}
										variant="accent"
									>
										{manualCheckPending ? t("startup.checkingAgain") : t("startup.checkAgain")}
									</TopbarButton>
								)}
							</div>
						)}
						{deviceError ? <p className="mt-2 text-xs text-destructive" role="alert">{deviceError}</p> : null}
						{startLogin.isError ? <p className="mt-2 text-xs text-destructive" role="alert">{startLogin.error.message}</p> : null}
						{terminal ? (
							<div className="mt-3 overflow-hidden rounded-lg border border-[var(--color-border-import-modal)] bg-terminal" data-testid="github-auth-terminal">
								<div className="flex min-h-9 items-center justify-between gap-3 border-b border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-modal)] px-3 py-1.5">
									<div className="min-w-0">
										<p className="truncate text-xs font-medium text-[var(--color-text-import-title)]">{terminal.title}</p>
										<p className="truncate text-[11px] text-[var(--color-text-import-muted)]">
											{t(loginEnded ? "startup.githubLoginStopped" : "startup.githubLoginRunning")}
										</p>
									</div>
									<TopbarButton aria-label={t("common.close")} className="!size-7 shrink-0" onClick={closeLogin} variant="icon">
										<X className="size-4" aria-hidden="true" />
									</TopbarButton>
								</div>
								<div className="h-[240px] min-h-0">
									<TerminalPane daemonReady={shell ? shell.daemonStatus.state === "ready" : true} focusRequested={loginFocusRequested && terminalState === "attached"} fontSize={12} onTerminalStateChange={handleTerminalState} terminalTarget={{ kind: "shell", handleId: terminal.handleId, generation: terminal.createdAt, title: terminal.title }} theme={theme} />
								</div>
							</div>
						) : null}
					</div>
				</div>
			</div>
		</div>
	);
}
