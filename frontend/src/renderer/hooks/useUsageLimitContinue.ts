import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	agentAccountName,
	agentAccountProviderFor,
	useAgentAccountActions,
	useAgentAccounts,
	useSessionAgentAccounts,
	type AgentAccount,
} from "./useAgentAccounts";
import { useSessionInterfaceTransitionStatus } from "./useSessionInterfaceTransition";

export interface UsageLimitContinue {
	/** The account the chat can continue on, by name. */
	account: string;
	/** A move to that account is under way; the prompt is sent once it lands. */
	pending: boolean;
	error?: string;
	start: () => void;
}

function hasQuota(account: AgentAccount): boolean {
	return account.signedIn && !(account.limits?.windows ?? []).some((window) => window.usedPercent >= 100);
}

/**
 * "Continue on <account>" for a chat stopped by its account's usage limit.
 *
 * Moving one session leaves every other session on the account that ran out,
 * so each limited chat offers another signed-in account of the same provider
 * that still has quota. The move is asynchronous; the failed prompt is sent
 * again only once it completes, on the new account. If the chat closes before
 * then, the limit itself says the account changed and offers Resume.
 */
export function useUsageLimitContinue({ sessionId, harness, limitedTurnId, enabled, retry }: {
	sessionId: string;
	harness: string;
	limitedTurnId?: string;
	/** Off for remote and cloud sessions: their accounts are not this computer's. */
	enabled: boolean;
	retry?: (turnId: string) => void | Promise<unknown>;
}): UsageLimitContinue | undefined {
	const { t } = useTranslation();
	const provider = agentAccountProviderFor(harness);
	const active = enabled && provider !== null && Boolean(limitedTurnId) && Boolean(retry);
	const current = useSessionAgentAccounts(active ? sessionId : undefined);
	const accounts = useAgentAccounts(active);
	const actions = useAgentAccountActions();
	const [move, setMove] = useState<{ transitionId: string; turnId: string }>();
	const [error, setError] = useState<string>();
	const status = useSessionInterfaceTransitionStatus(move ? sessionId : undefined);
	const retryRef = useRef(retry);
	retryRef.current = retry;

	const transition = status.transition;
	useEffect(() => {
		if (!move || !transition || transition.id !== move.transitionId) return;
		if (transition.phase === "completed") {
			setMove(undefined);
			void Promise.resolve(retryRef.current?.(move.turnId)).catch(() => undefined);
		} else if (transition.phase === "failed" || transition.phase === "cancelled" || transition.phase === "recovery_required") {
			setMove(undefined);
			setError(transition.errorDetail || t("settings.agentAccounts.switchFailed"));
		}
	}, [move, transition, t]);

	if (!active) return undefined;
	const currentId = provider ? current.data?.[provider] : undefined;
	const target = currentId
		? accounts.data?.find((account) => account.provider === provider && account.id !== currentId && hasQuota(account))
		: undefined;
	if (!target) return undefined;
	return {
		account: agentAccountName(target, t),
		pending: Boolean(move),
		error,
		start: () => {
			if (!limitedTurnId || move) return;
			setError(undefined);
			actions.switchSession(sessionId, target.id)
				.then((started) => setMove({ transitionId: started.id, turnId: limitedTurnId }))
				.catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));
		},
	};
}
