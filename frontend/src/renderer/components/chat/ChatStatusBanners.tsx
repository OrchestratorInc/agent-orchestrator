/**
 * Conversation state that is not a timeline entry.
 *
 * Each of these answers a question the timeline structurally cannot. A tool server
 * that failed to start produces no rows at all — the agent simply never calls those
 * tools, which reads as a choice. A provider demanding credentials leaves every
 * later turn failing for a reason that looks generic. A thread the provider has put
 * into `system_error` looks, from AO's side, like an agent that has gone quiet.
 *
 * The persistent ones live above the scroller rather than in it because they are
 * current state: scrolling away from them must not scroll away from the reason the
 * session is stuck. The MCP note is a one-time heads-up, so it docks by the composer
 * without displacing the conversation.
 */

import { memo, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CircleArrowUp, KeyRound, LoaderCircle, Plug, TriangleAlert, X } from "lucide-react";
import {
	agentAccountName,
	agentAccountProviderFor,
	useAgentAccountActions,
	useAgentAccounts,
	useSessionAgentAccounts,
	type AgentAccount,
	type AgentAccountLogin,
} from "../../hooks/useAgentAccounts";
import { useSettings } from "../../hooks/useSettings";
import { AgentAccountLoginDialog } from "../settings/AgentAccountsSection";
import { versionLabel } from "../../hooks/useHarnessUpdates";
import { cn, truncateMiddle } from "../../lib/utils";
import type { SessionStaleBinary } from "../../types/workspace";
import { Button } from "../ui/button";
import type { ConversationAccount, ConversationThreadState, McpServer } from "../../types/conversation";

/**
 * A current provider credential demand; dismissal affects presentation only.
 *
 * A session on a saved account offers to sign that account in again here, in its
 * own folder, and any session offers to continue on another signed-in account.
 * Both leave the failed turn for the user to retry.
 */
export const ReauthBanner = memo(function ReauthBanner({
	account,
	harness,
	sessionId,
	reasonInTimeline = false,
}: {
	account: ConversationAccount;
	harness: string;
	sessionId: string;
	reasonInTimeline?: boolean;
}) {
	const [dismissedFailure, setDismissedFailure] = useState<string>();
	const failure = account.authFailureId ?? `${harness}:${account.reauthRequiredAt}:${account.reauthReason}`;
	const required = account.authenticationState === "required" ||
		(account.authenticationState === undefined && Boolean(account.reauthRequiredAt));
	if (!required || dismissedFailure === failure) return null;
	return <ReauthNotice account={account} harness={harness} sessionId={sessionId} reasonInTimeline={reasonInTimeline}
		onDismiss={() => setDismissedFailure(failure)} />;
});

/** The shown notice. Its account reads run only while a failure is shown. */
function ReauthNotice({ account, harness, sessionId, reasonInTimeline, onDismiss }: {
	account: ConversationAccount;
	harness: string;
	sessionId: string;
	reasonInTimeline: boolean;
	onDismiss: () => void;
}) {
	const { t } = useTranslation();
	const provider = agentAccountProviderFor(harness);
	const sessionAccounts = useSessionAgentAccounts(provider ? sessionId : undefined);
	const accounts = useAgentAccounts(provider !== null);
	const actions = useAgentAccountActions();
	const actionsRef = useRef(actions);
	actionsRef.current = actions;
	const [login, setLogin] = useState<AgentAccountLogin>();
	const openLogin = useRef<string | undefined>(undefined);
	const verifying = useRef(false);
	const [loginNote, setLoginNote] = useState("");
	const [busy, setBusy] = useState<"login" | "verify" | "move" | null>(null);
	const [error, setError] = useState("");
	const [done, setDone] = useState<string>();
	// A sign-in left open when the chat goes away is cancelled, not abandoned.
	useEffect(() => () => {
		if (openLogin.current) void actionsRef.current.cancelLogin(openLogin.current).catch(() => undefined);
	}, []);
	const command = signInCommand(harness);
	const accountId = provider ? sessionAccounts.data?.[provider] : undefined;
	const current = accounts.data?.find((candidate) => candidate.id === accountId);
	const saved = current && !current.device ? current : undefined;
	const other = accountId
		? accounts.data?.find((candidate) => candidate.provider === provider && candidate.id !== accountId && candidate.signedIn)
		: undefined;
	const name = (target: AgentAccount) => agentAccountName(target, t);

	const run = async (kind: "login" | "move", action: () => Promise<void>) => {
		setBusy(kind);
		setError("");
		try {
			await action();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(null);
		}
	};
	const signIn = (target: AgentAccount) => run("login", async () => {
		setLoginNote("");
		const next = await actions.signInAgain(target.id);
		openLogin.current = next.operationId;
		setLogin(next);
	});
	const checkLogin = async (current: AgentAccountLogin) => {
		// The CLI exiting checks on its own; a click racing it would find the
		// sign-in already finished and report an error.
		if (openLogin.current !== current.operationId || verifying.current) return;
		verifying.current = true;
		setBusy("verify");
		try {
			const result = await actions.verifyLogin(current.operationId);
			if (result.status === "waiting") {
				setLoginNote(t("settings.agentAccounts.loginWaiting"));
				return;
			}
			openLogin.current = undefined;
			setLogin(undefined);
			if (result.status === "complete") setDone(t("chat.reauth.signedInAgain"));
			if (result.status === "duplicate") setError(t("settings.agentAccounts.loginDuplicate", { email: result.account?.email ?? "" }));
			if (result.status === "mismatch") setError(t("settings.agentAccounts.loginMismatch", { email: result.account?.email ?? "" }));
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			verifying.current = false;
			setBusy(null);
		}
	};
	const closeLogin = (current: AgentAccountLogin) => {
		openLogin.current = undefined;
		setLogin(undefined);
		void actions.cancelLogin(current.operationId).catch(() => undefined);
	};
	const moveTo = (target: AgentAccount) => run("move", async () => {
		await actions.switchSession(sessionId, target.id);
		await actions.refreshSession(sessionId);
		setDone(t("chat.reauth.movedTo", { account: name(target) }));
	});

	return (
		<div
			role="alert"
			className="flex shrink-0 items-start gap-2.5 border-b border-destructive/40 bg-destructive/10 px-4 py-3"
		>
			<KeyRound aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-destructive" />
			<div className="flex min-w-0 flex-col gap-1">
				<strong className="text-xs font-semibold text-destructive">
					{saved && !saved.signedIn ? t("chat.reauth.accountSignedOut", { account: name(saved) }) : "Provider authentication needs attention"}
				</strong>
				{!reasonInTimeline ? (
					<p className="text-xs leading-relaxed text-foreground">
						{account.reauthReason ??
							"The provider rejected this session's credentials."}
					</p>
				) : null}
				{done ? (
					<p role="status" className="text-xs leading-relaxed text-foreground">{done}</p>
				) : saved ? (
					<p className="text-xs leading-relaxed text-muted-foreground">{t("chat.reauth.savedAccountHint")}</p>
				) : (
				<p className="text-xs leading-relaxed text-muted-foreground">
					{command ? (
						<>
							Run{" "}
							<code className="rounded bg-background px-1 py-0.5 font-mono text-xs text-foreground">
								{command}
							</code>{" "}
							in a terminal if needed. This chat may need to reconnect before it can use updated credentials.
						</>
					) : (
						<>
							Sign in with the agent&rsquo;s own CLI if needed, then reconnect this chat.
						</>
					)}
				</p>
				)}
				{!done && (saved || other) ? (
					<div className="flex flex-wrap items-center gap-2 pt-1">
						{saved ? (
							<Button type="button" size="sm" variant="outline" data-terminal-focus-handoff="true" disabled={busy !== null}
								onClick={() => void signIn(saved)}>
								{busy === "login" ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : null}
								{t("settings.agentAccounts.signInAgain")}
							</Button>
						) : null}
						{other ? (
							<Button type="button" size="sm" variant="ghost" disabled={busy !== null} onClick={() => void moveTo(other)}>
								{busy === "move" ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : null}
								{t("chat.reauth.continueOn", { account: name(other) })}
							</Button>
						) : null}
					</div>
				) : null}
				{error ? <p role="alert" className="text-xs leading-relaxed text-destructive">{error}</p> : null}
			</div>
			<Button variant="ghost" size="icon" className="ml-auto size-6 shrink-0"
				aria-label="Dismiss authentication notice" title="Hide this notice; authentication state is unchanged"
				onClick={onDismiss}>
				<X aria-hidden="true" className="size-3.5" />
			</Button>
			{login ? (
				<AgentAccountLoginDialog login={login} note={loginNote} pending={busy === "verify"}
					onCheck={(current) => void checkLogin(current)} onClose={closeLogin} />
			) : null}
		</div>
	);
}

/**
 * The sign-in command for a harness, or nothing.
 *
 * Named per harness rather than described generically, because a user staring at a
 * blocked session wants the line to type. Unknown harnesses get the generic wording
 * instead of a guessed command that would fail.
 */
function signInCommand(harness: string): string | undefined {
	switch (harness) {
		case "codex":
			return "codex login";
		case "claude-code":
		case "claude":
			return "claude auth login";
		default:
			return undefined;
	}
}

/**
 * The provider's own view of the thread, when it is bad.
 *
 * Deliberately separate from the controller banner and worded so the two cannot be
 * confused: the controller is AO's connection to the agent process, this is what the
 * provider says about the conversation behind it. They disagree routinely — a
 * healthy controller can be attached to a thread the provider has already given up
 * on, and that combination is precisely the one a user cannot diagnose unaided.
 *
 * Only `system_error` and `closed` are drawn. `active`, `idle` and `not_loaded` are
 * the ordinary run of a session and a banner for each would be noise that teaches
 * readers to ignore this row.
 */
export const ThreadStateBanner = memo(function ThreadStateBanner({
	threadState,
}: {
	threadState: ConversationThreadState;
}) {
	const status = threadState.status;
	if (status !== "system_error" && status !== "closed") return null;

	const copy =
		status === "system_error"
			? {
					title: "The agent's thread hit an internal error",
					body: "The provider reported a fault in this thread, not in AO's connection to it. New turns will usually fail; the conversation and the worktree are kept.",
				}
			: {
					title: "The agent closed this thread",
					body: "The provider dropped the conversation on its side. AO kept the history, but the agent no longer holds it.",
				};

	return (
		<div
			role="alert"
			aria-atomic="true"
			className="flex shrink-0 items-start gap-2.5 border-b border-border bg-surface px-4 py-2.5"
		>
			<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-warning" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<strong className="text-xs font-medium text-warning">{copy.title}</strong>
				<span className="text-xs leading-snug text-muted-foreground">{copy.body}</span>
				{threadState.waitingOn?.length ? (
					<span className="text-xs leading-snug text-muted-foreground">
						Waiting on: {threadState.waitingOn.join(", ")}
					</span>
				) : null}
			</div>
		</div>
	);
});

const MCP_NOTICE_MS = 3_000;
const MCP_NOTICE_FADE_MS = 200;
const MCP_NOTICE_STORAGE_PREFIX = "ao:mcp-notice-shown:";
// The set covers pane remounts; storage keeps the note from replaying after a reload.
const mcpNoticeShownSessions = new Set<string>();

function mcpNoticeWasShown(sessionId: string): boolean {
	if (mcpNoticeShownSessions.has(sessionId)) return true;
	try {
		return window.localStorage.getItem(`${MCP_NOTICE_STORAGE_PREFIX}${sessionId}`) === "1";
	} catch {
		return false;
	}
}

function rememberMcpNotice(sessionId: string): void {
	mcpNoticeShownSessions.add(sessionId);
	try {
		window.localStorage.setItem(`${MCP_NOTICE_STORAGE_PREFIX}${sessionId}`, "1");
	} catch {
		// Best-effort: the in-memory set still covers this renderer's lifetime.
	}
}

/**
 * A brief, once-per-session note that some tool servers did not start.
 *
 * The agent never mentions tools it does not have, so without this a user sees a
 * worse answer with no cause. There is nothing to do from here, so say it once,
 * quietly, and get out of the way; a restarted session gets its own note.
 */
export const McpServerBanner = memo(function McpServerBanner({
	sessionId,
	incarnation,
	servers,
	placement = "above",
	active = true,
}: {
	sessionId: string;
	/** Which run of the session; a restart starts its tool servers again. */
	incarnation?: string;
	/** Only the broken ones. The caller filters, so an empty list means nothing to say. */
	servers: McpServer[];
	/** Relative to the composer, which the parent wraps in a positioned box. */
	placement?: "above" | "below";
	/** A hidden chat panel must not use up the session's one note. */
	active?: boolean;
}) {
	const { t } = useTranslation();
	const [phase, setPhase] = useState<"waiting" | "shown" | "fading" | "done">("waiting");
	const hasFailures = servers.length > 0;
	const noticeKey = incarnation ? `${sessionId}:${incarnation}` : sessionId;

	useEffect(() => {
		if (phase !== "waiting" || !active || !hasFailures || mcpNoticeWasShown(noticeKey)) return;
		rememberMcpNotice(noticeKey);
		setPhase("shown");
	}, [active, hasFailures, noticeKey, phase]);

	useEffect(() => {
		if (phase !== "shown" && phase !== "fading") return;
		const timer = window.setTimeout(
			() => setPhase(phase === "shown" ? "fading" : "done"),
			phase === "shown" ? MCP_NOTICE_MS : MCP_NOTICE_FADE_MS,
		);
		return () => window.clearTimeout(timer);
	}, [phase]);

	if (!hasFailures || (phase !== "shown" && phase !== "fading")) return null;
	const names = servers
		.map((server) => server.name.charAt(0).toUpperCase() + server.name.slice(1))
		.join(", ");

	return (
		<div
			role={phase === "shown" ? "status" : undefined}
			className={cn(
				"pointer-events-none absolute left-1/2 z-10 flex w-max max-w-full -translate-x-1/2 items-center gap-1.5 rounded-md bg-background px-2 py-0.5 text-xs text-muted-foreground transition-opacity duration-200 ease-out motion-reduce:transition-none",
				placement === "below" ? "top-full mt-1.5" : "bottom-full mb-1.5",
				phase === "fading" && "opacity-0",
			)}
		>
			<Plug aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate">
				{t("chat.mcpNotice.unavailable", { names, count: servers.length })}
			</span>
		</div>
	);
});

/**
 * A harness update replaced the release this session's agent runs. With
 * "move open sessions to new versions" on, the daemon restarts the agent on
 * the new release once it is idle, or before the next message; a failed
 * restart keeps the queued messages for a manual resume.
 */
export const VersionSwitchBanner = memo(function VersionSwitchBanner({
	staleBinary,
	hostId,
}: {
	staleBinary?: SessionStaleBinary;
	hostId?: string;
}) {
	// Most sessions are current; only a marked one reads the setting.
	return staleBinary?.version ? <VersionSwitchNotice staleBinary={staleBinary} hostId={hostId} /> : null;
});

function VersionSwitchNotice({ staleBinary, hostId }: { staleBinary: SessionStaleBinary; hostId?: string }) {
	const { t } = useTranslation();
	const { settings } = useSettings(hostId);
	const version = truncateMiddle(versionLabel(staleBinary.version));
	if (staleBinary.switchFailedAt) {
		return (
			<div role="alert" className="flex shrink-0 items-start gap-2.5 border-b border-border bg-surface px-4 py-2.5">
				<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-warning" />
				<div className="flex min-w-0 flex-col gap-0.5">
					<strong className="text-xs font-medium text-warning">{t("chat.versionSwitch.failedTitle", { version })}</strong>
					<span className="text-xs leading-snug text-muted-foreground">{t("chat.versionSwitch.failedBody")}</span>
				</div>
			</div>
		);
	}
	if (!settings?.versionRefresh) return null;
	return (
		<div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-surface px-4 py-1.5">
			<span className="inline-flex items-center gap-1.5 rounded-full border border-border px-2 py-0.5 text-caption text-muted-foreground">
				<CircleArrowUp aria-hidden="true" className="size-3 shrink-0 text-settings-accent" />
				{t("chat.versionSwitch.pending", { version })}
			</span>
			<span className="min-w-0 truncate text-caption text-muted-foreground">{t("chat.versionSwitch.pendingHint")}</span>
		</div>
	);
}
