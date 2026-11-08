import * as Dialog from "@radix-ui/react-dialog";
import { ChevronLeft, FolderClosed, MessageSquarePlus } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import aoLogo from "../assets/ao-mascot.png";
import { useCloseShellTerminal } from "../hooks/useShellTerminals";
import { useGitHubAuthRequirement, useGitHubAuthTerminal, useStartGitHubAuthTerminal, useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import type { TerminalSessionState } from "../hooks/useTerminalSession";
import { useShellMaybe } from "../lib/shell-context";
import { useResolvedTheme } from "../stores/ui-store";
import { TerminalPane } from "./TerminalPane";
import { GitHubRepoPicker, preloadGitHubRepos } from "./GitHubRepoPicker";
import { GitHubIcon } from "./GitHubIcon";
import { InstallAction, useInstallRunner } from "./InstallDependencyDialog";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Input } from "./ui/input";
import { Pressable } from "./ui/pressable";

/** Header shared by the steps after the welcome screen: back, title and subtext, close. */
function FirstRunHeader({ onBack, onClose, disabled, title, description }: { onBack: () => void; onClose: () => void; disabled?: boolean; title: string; description: string }) {
	const { t } = useTranslation();
	return (
		<div className="relative flex shrink-0 items-center gap-3 px-4 pt-3">
			<Button type="button" variant="outline" size="icon" aria-label={t("createProject.cloneBack")} disabled={disabled} onClick={onBack}>
				<ChevronLeft className="size-4" aria-hidden="true" />
			</Button>
			<div className="min-w-0 flex-1 pr-8">
				<Dialog.Title className="text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">{title}</Dialog.Title>
				<Dialog.Description className="mt-1 text-[13px] leading-5 text-muted-foreground">{description}</Dialog.Description>
			</div>
			<CloseButton aria-label={t("createProject.cloneClose")} disabled={disabled} onClick={onClose} />
		</div>
	);
}

/** First screen of the first-run prompt. */
export function FirstRunWelcome({ disabled, onNext }: { disabled?: boolean; onNext: () => void }) {
	const { t } = useTranslation();
	// Warm the repository list and avatars while the welcome screen is up.
	useEffect(() => {
		void preloadGitHubRepos();
	}, []);
	return (
		<div className="flex flex-col items-center px-6 pb-6 pt-8 text-center">
			<img alt="" className="size-16" draggable={false} src={aoLogo} />
			<Dialog.Title className="mt-5 text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">{t("firstRun.welcomeTitle")}</Dialog.Title>
			<Dialog.Description className="mt-2 max-w-[34ch] text-pretty text-[13px] leading-5 text-muted-foreground">{t("firstRun.welcomeBody")}</Dialog.Description>
			<Button type="button" variant="primary" className="mt-6 w-full" autoFocus disabled={disabled} onClick={onNext}>{t("firstRun.getStarted")}</Button>
		</div>
	);
}

export type GitHubState = {
	/** False until both the CLI and sign-in checks have answered. */
	known: boolean;
	/** The CLI is installed and signed in, so the user's repositories can be listed. */
	hasGitHub: boolean;
	/**
	 * The CLI is known to be missing or signed out, so a git-related action should
	 * first offer to install it or sign in. A probe that failed or timed out is
	 * not a reason to block anyone, so unknown never counts as needing setup.
	 */
	needsSetup: boolean;
};

export const NO_GITHUB_STATE: GitHubState = { known: true, hasGitHub: false, needsSetup: false };

/** Reads the GitHub CLI state for first-run children; the checks run only while this is mounted. */
export function WithGitHubState({ children }: { children: (state: GitHubState) => ReactNode }) {
	const { query, requirements } = useSystemRequirementsGate();
	const auth = useGitHubAuthRequirement();
	// A probe that errors, never starts (preview builds) or hangs must not leave
	// the source step on its placeholder: after a short wait, treat GitHub as unavailable.
	const [gaveUp, setGaveUp] = useState(false);
	useEffect(() => {
		const timer = window.setTimeout(() => setGaveUp(true), 4000);
		return () => window.clearTimeout(timer);
	}, []);
	const answered = (query.isSuccess || query.isError) && (auth.isSuccess || auth.isError);
	const known = answered || gaveUp;
	const installed = requirements.find((requirement) => requirement.id === "gh")?.satisfied !== false;
	const hasGitHub = known && installed && auth.data?.satisfied === true;
	const needsSetup = !installed || auth.data?.satisfied === false;
	return <>{children({ known, hasGitHub, needsSetup })}</>;
}

/** Install the GitHub CLI if it is missing, then sign in. Calls `onReady` once both are done. */
export function GitHubSetupPanel({ onReady, onSkip }: { onReady: () => void; onSkip?: () => void }) {
	const { t } = useTranslation();
	const { query, requirements } = useSystemRequirementsGate();
	const cliMissing = requirements.find((requirement) => requirement.id === "gh")?.satisfied === false;
	const [terminalState, setTerminalState] = useState<TerminalSessionState>("idle");
	const loginRunning = terminalState === "connecting" || terminalState === "attached" || terminalState === "reattaching";
	const auth = useGitHubAuthRequirement(loginRunning);
	const startLogin = useStartGitHubAuthTerminal();
	const terminalQuery = useGitHubAuthTerminal();
	const terminal = terminalQuery.data;
	const { mutate: closeTerminal } = useCloseShellTerminal();
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	const authRefetch = useRef(auth.refetch);
	authRefetch.current = auth.refetch;
	const readyRef = useRef(onReady);
	readyRef.current = onReady;
	const install = useInstallRunner(() => {
		void Promise.resolve(query.refetch())
			.then(() => authRefetch.current())
			.then((result) => {
				if (result.data?.satisfied) readyRef.current();
			});
	});
	const { inspect } = install;
	useEffect(() => {
		if (cliMissing) void inspect("gh");
	}, [cliMissing, inspect]);

	// Signed in, from the login terminal or from outside the app: nothing left to do.
	const signedIn = auth.data?.satisfied === true;
	useEffect(() => {
		if (!signedIn) return;
		if (terminal) closeTerminal(terminal.handleId, { onSuccess: terminalQuery.clear });
		readyRef.current();
	}, [signedIn, terminal, closeTerminal, terminalQuery.clear]);
	// The login process ending usually means it finished; confirm with a fresh check.
	useEffect(() => {
		if (terminalState === "exited" || terminalState === "error") void authRefetch.current();
	}, [terminalState]);

	const loginEnded = terminalState === "exited" || terminalState === "error";
	return (
		<div className="flex flex-col gap-3">
			<GitHubIcon className="mx-auto my-10 size-10 text-foreground" />
			{cliMissing ? (
				<InstallAction
					primaryLabel={t("startup.installGh")}
					disabled={install.running}
					job={install.jobFor("gh")}
					planChecked={install.inspectionFinished("gh")}
					error={install.target === "gh" ? install.startError : undefined}
					onInstall={() => void install.start("gh")}
					t={t}
				/>
			) : (
				<>
					<Button
						type="button"
						variant="primary"
						className="w-full"
						disabled={startLogin.isPending || (loginRunning && !loginEnded)}
						onClick={() => {
							if (loginEnded && terminal) closeTerminal(terminal.handleId, { onSuccess: () => { terminalQuery.clear(); setTerminalState("idle"); startLogin.mutate(); } });
							else startLogin.mutate();
						}}
					>
						{startLogin.isPending ? t("startup.githubLoginStarting") : loginEnded ? t("startup.githubLoginTryAgain") : t("startup.githubLogin")}
					</Button>
					{startLogin.isError ? <p className="text-xs text-destructive" role="alert">{startLogin.error.message}</p> : null}
					{terminal ? (
						<div className="overflow-hidden rounded-lg border border-[var(--color-border-import-modal)] bg-terminal" data-testid="github-auth-terminal">
							<div className="h-[220px] min-h-0">
								<TerminalPane daemonReady={shell ? shell.daemonStatus.state === "ready" : true} focusRequested={terminalState === "attached"} fontSize={12} onTerminalStateChange={setTerminalState} terminalTarget={{ kind: "shell", handleId: terminal.handleId, generation: terminal.createdAt, title: terminal.title }} theme={theme} />
							</div>
						</div>
					) : null}
				</>
			)}
			{onSkip ? <Button type="button" variant="quiet" className="w-full" disabled={install.running} onClick={onSkip}>{t("firstRun.ghContinue")}</Button> : null}
		</div>
	);
}

/** First-run step: GitHub setup for a clone. The user can continue without it, since public repos clone over plain git. */
export function FirstRunGitHubStep({ onDone, onBack, onClose }: { onDone: () => void; onBack: () => void; onClose: () => void }) {
	const { t } = useTranslation();
	const { requirements } = useSystemRequirementsGate();
	const cliMissing = requirements.find((requirement) => requirement.id === "gh")?.satisfied === false;
	return (
		<div>
			<FirstRunHeader
				onBack={onBack}
				onClose={onClose}
				title={t(cliMissing ? "firstRun.ghTitle" : "firstRun.ghLoginTitle")}
				description={t(cliMissing ? "firstRun.ghBody" : "firstRun.ghLoginBody")}
			/>
			<div className="px-4 pb-4 pt-4">
				<GitHubSetupPanel onReady={onDone} onSkip={onDone} />
			</div>
		</div>
	);
}

/** The "what do you want to start with" step. Same borderless tiles as the Add Project modal. */
export function FirstRunSources({
	disabled,
	gh,
	onBack,
	onClose,
	onPickRepo,
	onSubmitUrl,
	onOpenLocalFolder,
	onStartStandaloneAgent,
}: {
	disabled?: boolean;
	gh: GitHubState;
	onBack: () => void;
	onClose: () => void;
	onPickRepo: (remoteUrl: string) => void;
	onSubmitUrl: (remoteUrl: string) => void;
	onOpenLocalFolder?: () => void;
	onStartStandaloneAgent?: () => void;
}) {
	const { t } = useTranslation();
	const [url, setUrl] = useState("");
	const tile = (icon: ReactNode, label: string, hint: string, onClick?: () => void) => (
		<Pressable
			className="group flex flex-col items-start gap-5 rounded-lg bg-interactive-hover p-4 hover:bg-interactive-active disabled:pointer-events-none disabled:opacity-50"
			aria-label={label}
			disabled={disabled}
			onClick={onClick}
		>
			<span className="flex shrink-0 text-muted-foreground group-hover:text-foreground">{icon}</span>
			<span className="min-w-0">
				<span className="block text-[14px] font-medium text-foreground">{label}</span>
				<span className="mt-1 block text-pretty text-[12px] leading-5 text-muted-foreground">{hint}</span>
			</span>
		</Pressable>
	);
	return (
		<div>
			<FirstRunHeader onBack={onBack} onClose={onClose} disabled={disabled} title={t("firstRun.startProject")} description={t("firstRun.description")} />
			<div className="space-y-2 px-4 pb-4 pt-4">
				<p className="text-[13px] font-semibold text-[var(--color-text-import-title)]">{t(gh.hasGitHub ? "firstRun.pickRepo" : "createProject.cloneRepositoryUrl")}</p>
				{gh.hasGitHub ? (
					<GitHubRepoPicker disabled={disabled} value="" onChange={onPickRepo} />
				) : !gh.known ? (
					<div className="h-control-form rounded-md bg-[var(--color-bg-import-card)]" aria-hidden="true" />
				) : (
					<form
						className="flex gap-2"
						onSubmit={(event) => {
							event.preventDefault();
							if (url.trim()) onSubmitUrl(url.trim());
						}}
					>
						<Input
							autoCapitalize="none"
							autoComplete="off"
							className="bg-[var(--color-bg-import-card)] text-[13px]"
							disabled={disabled}
							placeholder={t("createProject.cloneRepositoryUrlPlaceholder")}
							spellCheck={false}
							value={url}
							onChange={(event) => setUrl(event.target.value)}
						/>
						<Button type="submit" variant="primary" disabled={disabled || !url.trim()}>{t("createProject.cloneContinue")}</Button>
					</form>
				)}
			</div>
			<p className="px-4 pb-2 text-[13px] font-semibold text-[var(--color-text-import-title)]">{t("firstRun.orStart")}</p>
			<div className="grid grid-cols-2 gap-3 px-4 pb-4 max-[520px]:grid-cols-1">
				{tile(<FolderClosed className="size-6" aria-hidden="true" strokeWidth={1.6} />, t("createProject.openLocal"), t("createProject.openLocalDesc"), onOpenLocalFolder)}
				{tile(<MessageSquarePlus className="size-6" aria-hidden="true" strokeWidth={1.6} />, t("home.newStandaloneAgent"), t("createProject.standaloneDesc"), onStartStandaloneAgent)}
			</div>
		</div>
	);
}

/** Springs the modal to each step's height. The content swaps instantly; only the size animates. */
export function AnimatedStepBody({ children }: { children: ReactNode }) {
	const reduceMotion = useReducedMotion();
	const bodyRef = useRef<HTMLDivElement>(null);
	const [height, setHeight] = useState<number | "auto">("auto");
	useLayoutEffect(() => {
		const body = bodyRef.current;
		if (!body || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => setHeight(body.offsetHeight));
		observer.observe(body);
		return () => observer.disconnect();
	}, []);
	return (
		<motion.div
			className="relative min-h-0 overflow-hidden"
			initial={false}
			animate={{ height }}
			transition={reduceMotion ? { duration: 0 } : { type: "spring", duration: 0.25, bounce: 0 }}
		>
			<div ref={bodyRef} className="flow-root">{children}</div>
		</motion.div>
	);
}
