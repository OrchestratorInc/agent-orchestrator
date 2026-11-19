import { RefreshCw, X } from "@/components/app-icons";
import { cn } from "@/lib/utils";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TerminalSessionState } from "../hooks/useTerminalSession";
import { useCloseShellTerminal } from "../hooks/useShellTerminals";
import { useGitHubAuthAutoLoginOffered, useGitHubAuthRequirement, useGitHubAuthTerminal, useStartGitHubAuthTerminal, useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { finishOnboardingAction, startOnboardingAction, useOnboardingStep } from "../lib/onboarding-telemetry";
import { useShellMaybe } from "../lib/shell-context";
import { useResolvedTheme, useUiStore } from "../stores/ui-store";
import { TerminalPane } from "./TerminalPane";
import { Button } from "./ui/button";

/**
 * Signing the GitHub CLI in from onboarding: an embedded `gh auth login`
 * terminal. GitHub is not required for local work, but signing in before the
 * first task prevents a late PR-creation failure inside an agent session.
 *
 * The state lives here; the caller places its two parts, the actions
 * (GitHubLoginActions) and the terminal they open (GitHubLoginTerminal).
 */
export function useGitHubLogin() {
	const { t } = useTranslation();
	const gate = useSystemRequirementsGate();
	const startLogin = useStartGitHubAuthTerminal();
	const autoLogin = useGitHubAuthAutoLoginOffered();
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
	const terminalRef = useRef(terminal);
	const refetchAuthRef = useRef(authQuery.refetch);
	const exitCheckedTerminalRef = useRef<string | null>(null);
	const completedTerminalRef = useRef<string | null>(null);
	const [focusRequested, setFocusRequested] = useState(false);
	const [checkPending, setCheckPending] = useState(false);
	const gh = (gate.requirements ?? []).find((requirement) => requirement.id === "gh");
	const auth = authQuery.data;

	terminalRef.current = terminal;
	refetchAuthRef.current = authQuery.refetch;
	const onTerminalState = useCallback((state: TerminalSessionState) => {
		const active = terminalRef.current;
		setTerminalStatus({ handleId: active?.handleId ?? null, state });
		if (!active || (state !== "exited" && state !== "error")) return;
		if (exitCheckedTerminalRef.current === active.handleId) return;
		exitCheckedTerminalRef.current = active.handleId;
		void refetchAuthRef.current().then((result) => {
			// The login ended and GitHub still says signed out.
			if (result.data && !result.data.satisfied) finishOnboardingAction("gh_sign_in", "failed", { target: "gh", reason: "not_signed_in" });
		});
	}, []);
	// Signed in, from the terminal or outside the app: the attempt in progress succeeded.
	useEffect(() => {
		if (auth?.satisfied) finishOnboardingAction("gh_sign_in", "succeeded", { target: "gh" });
	}, [auth?.satisfied]);
	useEffect(() => {
		if (startLogin.isError) finishOnboardingAction("gh_sign_in", "failed", { target: "gh", reason: "start_failed" });
	}, [startLogin.isError]);
	useEffect(() => {
		if (!auth?.satisfied || !terminal) return;
		if (completedTerminalRef.current === terminal.handleId) return;
		completedTerminalRef.current = terminal.handleId;
		showGlobalToast(t("startup.githubConnected"));
		closeTerminal(terminal.handleId, { onSuccess: terminalQuery.clear });
	}, [auth?.satisfied, closeTerminal, showGlobalToast, t, terminal, terminalQuery.clear]);
	useEffect(() => {
		if (
			!auth ||
			auth.satisfied ||
			gh?.satisfied !== true ||
			terminal ||
			startLogin.isPending ||
			autoLogin.offered
		) {
			return;
		}
		autoLogin.markOffered();
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth", auto: true });
		startLogin.mutate();
	}, [auth, autoLogin.markOffered, autoLogin.offered, gh?.satisfied, startLogin.isPending, startLogin.mutate, terminal]);

	const signedOut = gh?.satisfied === true && Boolean(auth && !auth.satisfied);
	useOnboardingStep("github_auth", signedOut);

	const open = () => {
		setFocusRequested(true);
		autoLogin.markOffered();
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth" });
		startLogin.mutate();
	};
	const retry = () => {
		if (!terminal) return;
		setFocusRequested(true);
		closeTerminal(terminal.handleId, {
			onSuccess: () => {
				terminalQuery.clear();
				startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth" });
				startLogin.mutate();
			},
		});
	};
	const checkAgain = async () => {
		setCheckPending(true);
		try {
			await authQuery.refetch();
		} finally {
			setCheckPending(false);
		}
	};
	const close = () => {
		setFocusRequested(false);
		if (!terminal) return;
		// Closed by the person before GitHub said signed in.
		finishOnboardingAction("gh_sign_in", "cancelled", { target: "gh" });
		closeTerminal(terminal.handleId, { onSuccess: terminalQuery.clear });
	};
	return {
		/** Installed, but not signed in: the only time either part shows. */
		signedOut,
		starting: startLogin.isPending,
		startError: startLogin.isError ? startLogin.error.message : undefined,
		terminal,
		terminalState,
		loginRunning,
		loginEnded,
		focusRequested,
		checkPending,
		onTerminalState,
		open,
		retry,
		checkAgain,
		close,
	};
}

export type GitHubLogin = ReturnType<typeof useGitHubLogin>;

/** Check again (a refresh icon) and Sign in (or Try again once a login
 * ended), side by side; Check again steps aside while a login runs. */
export function GitHubLoginActions({ login }: { login: GitHubLogin }) {
	const { t } = useTranslation();
	if (!login.signedOut) return null;
	return (
		<span className="flex items-center gap-2">
			{login.loginRunning ? null : (
				<Button
					type="button"
					size="icon-sm"
					variant="quiet"
					aria-label={login.checkPending ? t("startup.checkingAgain") : t("startup.checkAgain")}
					title={t("startup.checkAgain")}
					disabled={login.checkPending}
					onClick={() => void login.checkAgain()}
				>
					<RefreshCw className={cn("size-icon-md", login.checkPending && "animate-spin")} aria-hidden="true" />
				</Button>
			)}
			<Button
				type="button"
				size="sm"
				variant="primary"
				disabled={login.starting || Boolean(login.terminal && !login.loginEnded)}
				onClick={() => (login.loginEnded ? login.retry() : login.open())}
			>
				{login.starting
					? t("startup.githubLoginStarting")
					: login.loginEnded
						? t("startup.githubLoginTryAgain")
						: t("firstRun.signIn")}
			</Button>
		</span>
	);
}

/** The `gh auth login` terminal once a login starts, and why one couldn't. */
export function GitHubLoginTerminal({ login }: { login: GitHubLogin }) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	if (!login.signedOut || (!login.terminal && !login.startError)) return null;
	const { terminal } = login;
	return (
		<div className="flex flex-col gap-2" role="status">
			{login.startError ? <p className="text-xs text-destructive" role="alert">{login.startError}</p> : null}
			{terminal ? (
				<div className="overflow-hidden rounded-lg border border-[var(--color-border-import-modal)] bg-terminal" data-testid="github-auth-terminal">
					<div className="flex min-h-9 items-center justify-between gap-3 border-b border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-modal)] px-3 py-1.5">
						<div className="min-w-0">
							<p className="truncate text-xs font-medium text-[var(--color-text-import-title)]">{terminal.title}</p>
							<p className="truncate text-[11px] text-[var(--color-text-import-muted)]">
								{t(login.loginEnded ? "startup.githubLoginStopped" : "startup.githubLoginRunning")}
							</p>
						</div>
						<Button type="button" size="icon-sm" variant="quiet" aria-label={t("common.close")} className="!size-7 shrink-0" onClick={login.close}>
							<X className="size-4" aria-hidden="true" />
						</Button>
					</div>
					<div className="h-[240px] min-h-0">
						<TerminalPane
							daemonReady={shell ? shell.daemonStatus.state === "ready" : true}
							focusRequested={login.focusRequested && login.terminalState === "attached"}
							fontSize={12}
							onTerminalStateChange={login.onTerminalState}
							terminalTarget={{ kind: "shell", handleId: terminal.handleId, generation: terminal.createdAt, title: terminal.title }}
							theme={theme}
						/>
					</div>
				</div>
			) : null}
		</div>
	);
}
