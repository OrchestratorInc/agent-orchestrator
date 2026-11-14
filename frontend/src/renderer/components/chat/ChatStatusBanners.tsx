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
	hostId?: string;
	cloud?: boolean;
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
	}
}

export const McpServerBanner = memo(function McpServerBanner({
	sessionId,
	incarnation,
	servers,
	placement = "above",
	active = true,
}: {
	sessionId: string;
	incarnation?: string;
	servers: McpServer[];
	placement?: "above" | "below";
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

export const VersionSwitchBanner = memo(function VersionSwitchBanner({
	staleBinary,
	hostId,
	agentStopped,
}: {
	staleBinary?: SessionStaleBinary;
	hostId?: string;
	agentStopped?: boolean;
}) {
	return staleBinary?.version ? <VersionSwitchNotice staleBinary={staleBinary} hostId={hostId} agentStopped={agentStopped} /> : null;
});

function VersionSwitchNotice({ staleBinary, hostId, agentStopped }: { staleBinary: SessionStaleBinary; hostId?: string; agentStopped?: boolean }) {
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
				{t(agentStopped ? "chat.versionSwitch.installed" : "chat.versionSwitch.pending", { version })}
			</span>
			<span className="min-w-0 truncate text-caption text-muted-foreground">
				{t(agentStopped ? "chat.versionSwitch.stoppedHint" : "chat.versionSwitch.pendingHint")}
			</span>
		</div>
	);
}
