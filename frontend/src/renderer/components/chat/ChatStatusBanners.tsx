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

import { memo, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { CircleArrowUp, Download, KeyRound, Plug, TriangleAlert } from "@/components/app-icons";
import { agentAccountProviderFor, useSessionAgentAccounts } from "../../hooks/useAgentAccounts";
import { useSettings } from "../../hooks/useSettings";
import { versionLabel } from "../../hooks/useHarnessUpdates";
import { agentLabel } from "../../lib/agent-options";
import type { AgentBlock } from "../../lib/session-import";
import { labelForHost } from "../../lib/host-clients";
import { cn, truncateMiddle } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import type { SessionStaleBinary } from "../../types/workspace";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import type { ConversationAccount, ConversationThreadState, McpServer } from "../../types/conversation";

export function ImportedAgentBlocked({ agent, block }: { agent: string; block: AgentBlock }) {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const signedOut = block === "signed_out";
	const Icon = signedOut ? KeyRound : Download;
	return (
		<div role="status" className="flex items-center gap-2.5 rounded-[var(--radius-chat-composer)] border border-border bg-surface px-4 py-2.5">
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<p className="min-w-0 flex-1 text-sm text-foreground">
				{t(signedOut ? "sessionImport.blockedSignedOut" : "sessionImport.blockedMissing", { agent: agentLabel(agent) })}
			</p>
			<Button
				type="button"
				size="sm"
				variant="primary"
				className="shrink-0"
				onClick={() => openGlobalSettings("harness", { focusAgentId: agent, startLogin: signedOut, preserveProject: true })}
			>
				{t(signedOut ? "sessionImport.signIn" : "sessionImport.install")}
			</Button>
		</div>
	);
}

/**
 * The provider refused this session's login.
 *
 * An expired login is routine, so the notice says so plainly and offers one way
 * out: the Harness page, which signs in on the computer that runs the session,
 * a remote host or the cloud connection included. The failed turn stays for the
 * user to retry.
 */
export const ReauthBanner = memo(function ReauthBanner({
	account,
	harness,
	sessionId,
	hostId,
	cloud = false,
	onLogIn,
}: {
	account: ConversationAccount;
	harness: string;
	sessionId?: string;
	/** The remote host that runs the session; omitted for this computer's. */
	hostId?: string;
	/** The session runs in the cloud, so its login is the cloud connection's. */
	cloud?: boolean;
	/** Starts the login so that it brings the user back to this chat once it takes. */
	onLogIn?: () => void;
}) {
	const { t } = useTranslation();
	const required = account.authenticationState === "required" ||
		(account.authenticationState === undefined && Boolean(account.reauthRequiredAt));
	if (!required) return null;
	const agent = agentLabel(harness);
	const host = hostId ? labelForHost(hostId) ?? hostId : undefined;
	return (
		<div role="status" className="flex shrink-0 items-center gap-2.5 border-b border-border bg-surface px-4 py-2">
			<KeyRound aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<p className="min-w-0 flex-1 text-xs leading-snug text-foreground">
				{host ? t("chat.reauth.expiredOnHost", { agent, host }) : t("chat.reauth.expired", { agent })}
			</p>
			{/* A chat signs in the session's own account itself, so it needs no lookup here. */}
			{onLogIn ? (
				<Button type="button" size="sm" variant="secondary" className="shrink-0" onClick={onLogIn}>
					{t("chat.reauth.logInAgain")}
				</Button>
			) : <ReauthLogInButton harness={harness} sessionId={sessionId} hostId={hostId} cloud={cloud} />}
		</div>
	);
});

function ReauthLogInButton({ harness, sessionId, hostId, cloud }: { harness: string; sessionId?: string; hostId?: string; cloud: boolean }) {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const provider = !hostId && !cloud && sessionId ? agentAccountProviderFor(harness) : null;
	const sessionAccounts = useSessionAgentAccounts(provider ? sessionId : undefined);
	const sessionAccountId = provider ? sessionAccounts.data?.[provider] : undefined;
	return (
		<Button type="button" size="sm" variant="secondary" className="shrink-0" disabled={provider !== null && sessionAccounts.isPending}
			onClick={() => openGlobalSettings("harness", {
				focusAgentId: harness,
				...(hostId ? { hostId } : {}),
				harnessView: cloud ? "cloud" : "local",
				...(sessionAccountId ? { loginAccountId: sessionAccountId } : { startLogin: true }),
				preserveProject: true,
			})}>
			{t("chat.reauth.logInAgain")}
		</Button>
	);
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
					body: "The provider reported a fault in this thread, not in Orchestrator's connection to it. New turns will usually fail; the conversation and the worktree are kept.",
				}
			: {
					title: "The agent closed this thread",
					body: "The provider dropped the conversation on its side. Orchestrator kept the history, but the agent no longer holds it.",
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
 * A harness update replaced the release this session's agent runs, and moving
 * it to the new release failed. The queued messages are kept for a manual
 * resume. A failure needs the user, so it stays a banner above the
 * conversation; the routine wait lives in the topbar (VersionSwitchChip).
 */
export const VersionSwitchBanner = memo(function VersionSwitchBanner({ staleBinary }: { staleBinary?: SessionStaleBinary }) {
	const { t } = useTranslation();
	if (!staleBinary?.version || !staleBinary.switchFailedAt) return null;
	const version = truncateMiddle(versionLabel(staleBinary.version));
	return (
		<div role="alert" className="flex shrink-0 items-start gap-2.5 border-b border-border bg-surface px-4 py-2.5">
			<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-warning" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<strong className="text-xs font-medium text-warning">{t("chat.versionSwitch.failedTitle", { version })}</strong>
				<span className="text-xs leading-snug text-muted-foreground">{t("chat.versionSwitch.failedBody")}</span>
			</div>
		</div>
	);
});

/**
 * The routine side of a harness update, in the chat's topbar. With "move open
 * sessions to new versions" on, the daemon restarts the agent on the new
 * release once it is idle, or before the next message. That is ordinary
 * state, not a problem, so it is one small chip rather than a strip across
 * the conversation; its popover says what happened and why it waits. A
 * stopped agent is never moved on its own: resuming it starts the new release.
 */
export const VersionSwitchChip = memo(function VersionSwitchChip({
	staleBinary,
	hostId,
	harness,
	agentStopped,
}: {
	staleBinary?: SessionStaleBinary;
	hostId?: string;
	/** The session's harness, named in the popover title. */
	harness: string;
	/** The agent controller stopped and waits for the user to resume it. */
	agentStopped?: boolean;
}) {
	// Most sessions are current; only a marked one reads the setting.
	return staleBinary?.version && !staleBinary.switchFailedAt
		? <VersionSwitchPopover staleBinary={staleBinary} hostId={hostId} harness={harness} agentStopped={agentStopped} />
		: null;
});

function VersionSwitchPopover({ staleBinary, hostId, harness, agentStopped }: { staleBinary: SessionStaleBinary; hostId?: string; harness: string; agentStopped?: boolean }) {
	const { t } = useTranslation();
	const { settings } = useSettings(hostId);
	if (!settings?.versionRefresh) return null;
	const version = truncateMiddle(versionLabel(staleBinary.version));
	const title = t("chat.versionSwitch.title", { agent: agentLabel(harness), version });
	const chip = t("chat.versionSwitch.chip");
	// Its own action group: the class gives it the topbar's no-drag region,
	// control height and label compaction, like the session's other actions.
	return (
		<div className="workspace-topbar-actions">
			<Popover>
				<PopoverTrigger asChild>
					<Button type="button" variant="secondary" size="xs" aria-label={`${chip}: ${title}`} data-priority="secondary" data-version-switch-chip="">
						<CircleArrowUp aria-hidden="true" className="size-icon-sm shrink-0" />
						<span data-compact-label="">{chip}</span>
					</Button>
				</PopoverTrigger>
				<PopoverContent align="end" aria-label={title} className="flex w-80 max-w-[calc(100vw-1rem)] flex-col gap-2 p-3.5">
					<h2 className="text-control font-medium text-foreground">{title}</h2>
					<p className="text-xs leading-normal text-muted-foreground">
						{t(agentStopped ? "chat.versionSwitch.stoppedHint" : "chat.versionSwitch.pendingBody")}
					</p>
					{agentStopped ? null : <p className="text-xs leading-normal text-muted-foreground">{t("chat.versionSwitch.waitReason")}</p>}
				</PopoverContent>
			</Popover>
		</div>
	);
}
