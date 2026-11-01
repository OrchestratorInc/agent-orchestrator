import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentAccountName, agentAccountProviderFor, useAgentAccountActions, useAgentAccountLabel, useAgentAccounts, useSessionAgentAccounts } from "../hooks/useAgentAccounts";
import { interfaceTransitionIsActive, useSessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";
import type { WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { AccountLimits } from "./settings/AgentAccountsSection";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

/** The account the session's agent runs under, its limits, and the move to
 * another account. A move is the session's durable controller handoff: the
 * agent restarts in the same conversation on the new account, or stays on the
 * old one if that fails. Moving a working agent stops its turn after asking. */
export function SessionAccountSummary({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const provider = agentAccountProviderFor(session.provider);
	const accounts = useAgentAccounts();
	const current = useSessionAgentAccounts(provider ? session.id : undefined);
	const actions = useAgentAccountActions();
	const label = useAgentAccountLabel();
	const { transition } = useSessionInterfaceTransitionStatus(provider ? session.id : undefined);
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

	if (!provider || session.cloud || session.isTerminated) return null;
	const options = (accounts.data ?? []).filter((account) => account.provider === provider);
	const currentId = current.data?.[provider];
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
				: message;

	return <section className="mt-3 border-t border-(--color-border-settings-input) pt-3" data-testid="session-account-summary">
		<div className="flex items-center justify-between gap-3">
			<p className="text-xs text-muted-foreground">{t("settings.agentAccounts.sessionAccount")}</p>
			<div className="flex min-w-0 items-center gap-2">
				{moving ? <LoaderCircle className="size-3.5 animate-spin text-muted-foreground" aria-hidden="true" /> : null}
				<Select value={currentId ?? ""} disabled={moving || options.length < 2} onValueChange={(value) => void move(value)}>
					<SelectTrigger size="sm" className="max-w-56" aria-label={t("settings.agentAccounts.sessionAccount")}><SelectValue /></SelectTrigger>
					<SelectContent align="end">
						{options.map((option) => <SelectItem key={option.id} value={option.id} disabled={!option.signedIn}>{agentAccountName(option, t)}</SelectItem>)}
					</SelectContent>
				</Select>
			</div>
		</div>
		{status ? <p className="mt-2 text-xs text-muted-foreground" role="status">{status}</p> : null}
		{account ? <AccountLimits account={account} /> : null}
		{confirming ? <ConfirmDialog open
			title={t("settings.agentAccounts.stopAndMoveTitle")}
			description={session.activity?.state === "blocked"
				? t("settings.agentAccounts.stopAndMoveDeciding", { email: label(confirming) })
				: t("settings.agentAccounts.stopAndMoveWorking", { email: label(confirming) })}
			confirmLabel={t("settings.agentAccounts.stopAndMove")}
			onConfirm={() => void move(confirming, true)}
			onOpenChange={(open) => { if (!open) setConfirming(null); }}
		/> : null}
	</section>;
}
