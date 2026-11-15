import type { QueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { clearCloudChatCaches } from "../lib/cloud-chat-history";
import { useUiStore } from "../stores/ui-store";

export function clearPreviousAccountState(queryClient: QueryClient): void {
	const mutations = queryClient.getMutationCache();
	for (const mutation of mutations.getAll()) {
		if (mutation.state.status === "error") mutations.remove(mutation);
	}
	useUiStore.setState({ orchestratorStartupErrors: {}, orchestratorReplacementErrors: {} });
	clearCloudChatCaches(queryClient);
}

export function useCloudAccountSwitchReset({
	userId,
	ready,
	queryClient,
	onLocalPage,
	leavePage,
}: {
	userId: string | null;
	ready: boolean;
	queryClient: QueryClient;
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
