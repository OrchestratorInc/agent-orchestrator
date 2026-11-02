import type { QueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useUiStore } from "../stores/ui-store";

/**
 * Drops what the previous cloud account left on screen: failed actions (an
 * orchestrator spawn its plan refused) and per-project startup errors.
 */
export function clearPreviousAccountState(queryClient: QueryClient): void {
	const mutations = queryClient.getMutationCache();
	for (const mutation of mutations.getAll()) {
		if (mutation.state.status === "error") mutations.remove(mutation);
	}
	useUiStore.setState({ orchestratorStartupErrors: {}, orchestratorReplacementErrors: {} });
}

/**
 * When the signed-in cloud user changes (sign-out, or another account), clears
 * the previous account's leftovers and leaves a page that is not one of this
 * machine's local projects, so the new account never sees the old account's
 * project or its errors. The first known user (app start) and token refreshes
 * of the same user change nothing.
 */
export function useCloudAccountSwitchReset({
	userId,
	ready,
	queryClient,
	onLocalPage,
	leavePage,
}: {
	/** The signed-in cloud user, or null when signed out. */
	userId: string | null;
	/** False while the cloud session is still loading. */
	ready: boolean;
	queryClient: QueryClient;
	/** True when the current page is not a cloud project or session. */
	onLocalPage: boolean;
	leavePage: () => void;
}): void {
	const previousUser = useRef<string | null | undefined>(undefined);
	const latest = useRef({ onLocalPage, leavePage });
	latest.current = { onLocalPage, leavePage };
	useEffect(() => {
		if (!ready) return;
		const before = previousUser.current;
		previousUser.current = userId;
		if (before === undefined || before === userId) return;
		clearPreviousAccountState(queryClient);
		if (!latest.current.onLocalPage) latest.current.leavePage();
	}, [queryClient, ready, userId]);
}
