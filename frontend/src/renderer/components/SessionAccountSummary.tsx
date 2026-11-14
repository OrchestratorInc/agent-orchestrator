import { LoaderCircle } from "@/components/app-icons";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountLabel, agentAccountProviderFor, useAgentAccountActions, useAgentAccountLabel, useAgentAccounts, useSessionAgentAccounts, withoutSameLogins } from "../hooks/useAgentAccounts";
import { interfaceTransitionIsActive, useSessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";
import { LOCAL_HOST } from "../lib/hosts";
import type { WorkspaceSession } from "../types/workspace";
import type { AccountChoice } from "./chat/AgentSwitchPicker";
import { ConfirmDialog } from "./ConfirmDialog";
import { AccountLimits } from "./settings/AgentAccountsSection";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

/** The account a session's agent runs under, and the move to another account.
 * A move is the session's durable controller handoff: the agent restarts in the
 * same conversation on the new account, or stays on the old one if that fails.
 * Moving a working agent stops its turn after asking, through `dialog`. */
export function useSessionAccountSwitch(session: WorkspaceSession, sessionHostId = session.hostId) {
	const { t } = useTranslation();
	const hostId = sessionHostId === LOCAL_HOST ? undefined : sessionHostId;
	const provider = agentAccountProviderFor(session.provider);
	// Only a live Claude Code or Codex session on a daemon has an account to show.
	const shown = Boolean(provider) && !session.cloud && !session.isTerminated;
	const shownId = shown ? session.id : undefined;
	const accounts = useAgentAccounts(shown, hostId);
	const current = useSessionAgentAccounts(shownId, hostId);
	const actions = useAgentAccountActions(hostId);
	const label = useAgentAccountLabel(shown, hostId);
	const { transition } = useSessionInterfaceTransitionStatus(shownId, hostId);
	const [message, setMessage] = useState("");
	const [confirming, setConfirming] = useState<string | null>(null);
	const accountMove = transition?.targetAccountId ? transition : undefined;
	const moving = interfaceTransitionIsActive(accountMove);
	const settledPhase = accountMove && !moving ? accountMove.phase : undefined;

	// The binding commits at the end of the move; reread it once the move settles.
	useEffect(() => {
		if (settledPhase) void actions.refreshSession(session.id);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [settledPhase, accountMove?.id, session.id]);

	const currentId = provider ? current.data?.[provider] : undefined;
	const options = useMemo(() => provider
		? withoutSameLogins((accounts.data ?? []).filter((account) => account.provider === provider), currentId)
		: [], [accounts.data, currentId, provider]);
	const account = options.find((option) => option.id === currentId);
	const working = session.activity?.state === "active" || session.activity?.state === "blocked";
	// "Moved from … to …" answers the move; the next message makes it history.
	const messagedSinceMove = Boolean(accountMove && session.lastUserMessageAt &&
		Date.parse(session.lastUserMessageAt) > Date.parse(accountMove.updatedAt));

	const move = async (accountId: string, stopTurn = false) => {
		if (accountId === currentId) return;
		// A working agent may only be waiting on the old account's limit; moving
		// stops that turn, so ask first.
		if (working && !stopTurn) {
			setConfirming(accountId);
			return;
		}
		setConfirming(null);
		setMessage("");
		try {
			await actions.switchSession(session.id, accountId, stopTurn);
		} catch (err) {
			setMessage(err instanceof Error ? err.message : String(err));
		}
	};


	const status = moving
		? t("settings.agentAccounts.switching", { email: label(accountMove?.targetAccountId) })
		: settledPhase === "completed" && accountMove?.targetAccountId === currentId && !messagedSinceMove
			? t("settings.agentAccounts.moved", { from: label(accountMove?.sourceAccountId), to: label(currentId) })
			: settledPhase === "failed" || settledPhase === "recovery_required"
				? accountMove?.errorDetail || t("settings.agentAccounts.switchFailed")
				: "";

	const moveRef = useRef(move);
	moveRef.current = move;
	// Only a subscription takes a session: the daemon refuses a move to an API-key login.
	const choice = useMemo<AccountChoice | undefined>(() => {
		if (!shown || !provider) return undefined;
		return {
			options: options.map((option) => ({ id: option.id, label: agentAccountLabel(option, options, t), disabled: !option.signedIn })),
			value: currentId,
			onPick: (id) => void moveRef.current(id),
			disabled: moving || options.length < 2,
		};
	}, [currentId, moving, options, provider, shown, t]);

	const dialog = confirming ? <ConfirmDialog open
		title={t("settings.agentAccounts.stopAndMoveTitle")}
		description={session.activity?.state === "blocked"
			? t("settings.agentAccounts.stopAndMoveDeciding", { email: label(confirming) })
			: t("settings.agentAccounts.stopAndMoveWorking", { email: label(confirming) })}
		confirmLabel={t("settings.agentAccounts.stopAndMove")}
		onConfirm={() => void move(confirming, true)}
		onOpenChange={(open) => { if (!open) setConfirming(null); }}
	/> : null;

	return { shown, choice, account, moving, status, dialog, error: message, clearError: () => setMessage("") };
}

export function SessionAccountSummary({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const { shown, choice, account, moving, status, dialog, error } = useSessionAccountSwitch(session, hostId ?? session.hostId);
	if (!shown || !choice) return null;

	return <section className="mt-3 border-t border-(--color-border-settings-input) pt-3" data-testid="session-account-summary">
		<div className="flex items-center justify-between gap-3">
			<p className="text-xs text-muted-foreground">{t("settings.agentAccounts.sessionAccount")}</p>
			<div className="flex min-w-0 items-center gap-2">
				{moving ? <LoaderCircle className="size-3.5 animate-spin text-muted-foreground" aria-hidden="true" /> : null}
				<Select value={choice.value ?? ""} disabled={choice.disabled} onValueChange={choice.onPick}>
					<SelectTrigger size="sm" className="max-w-56" aria-label={t("settings.agentAccounts.sessionAccount")}><SelectValue /></SelectTrigger>
					<SelectContent align="end">
						{choice.options.map((option) => <SelectItem key={option.id} value={option.id} disabled={option.disabled}>{option.label}</SelectItem>)}
					</SelectContent>
				</Select>
			</div>
		</div>
		{error ? <p className="mt-2 text-xs text-muted-foreground" role="status">{error}</p> : null}
		{status ? <p className="mt-2 text-xs text-muted-foreground" role="status">{status}</p> : null}
		{account ? <AccountLimits account={account} /> : null}
		{dialog}
	</section>;
}
