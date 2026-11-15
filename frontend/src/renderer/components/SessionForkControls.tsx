import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { GitFork } from "@/components/app-icons";
import { createContext, useCallback, useContext, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useHostConnection } from "../hooks/useHostConnection";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export const ForkSessionContext = createContext<WorkspaceSession | null>(null);
export const ForkableTurnContext = createContext<string | null>(null);
export const LatestForkTurnContext = createContext<string | undefined>(undefined);

export function useSessionForkOptions(session: WorkspaceSession | undefined, enabled = true) {
	const { t } = useTranslation();
	const host = useHostConnection(session?.hostId);
	return useQuery({
		enabled: enabled && Boolean(session),
		queryKey: ["session-fork-options", session?.hostId ?? "", host.isRemote ? host.baseUrl ?? "offline" : "", session?.id ?? "", session?.status, session?.provisionState ?? ""],
		queryFn: async () => {
			const client = clientForSessionHost(session?.hostId);
			const { data, error } = await client.GET("/api/v1/sessions/{sessionId}/fork-options", {
				params: { path: { sessionId: session?.id ?? "" } },
			});
			if (error || !data) throw apiErrorException(error, t("sessionFork.unavailable"));
			return data;
		},
	});
}

function useSessionFork(session: WorkspaceSession, turnId?: string) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const requestId = useRef<string | null>(null);
	const inFlight = useRef(false);
	const options = useSessionForkOptions(session);
	const fork = useMutation({
		mutationFn: async () => {
			requestId.current ??= crypto.randomUUID();
			const client = clientForSessionHost(session.hostId);
			const { data, error } = await client.POST("/api/v1/sessions/{sessionId}/fork", {
				params: { path: { sessionId: session.id } },
				body: { clientRequestId: requestId.current, ...(turnId ? { turnId } : {}) },
			});
			if (error || !data) throw apiErrorException(error, t("sessionFork.failed"));
			return data;
		},
		onSuccess: (data) => {
			requestId.current = null;
			void queryClient.invalidateQueries({ queryKey: ["session-fork-options"] });
			void navigate(sessionNavigateTarget(session.workspaceId, data.sessionId, session.hostId));
		},
	});
	const refusal = options.data?.refusal?.message;
	const unavailable = !options.data?.allowed || fork.isPending;
	const tooltip = refusal ?? (options.error ? options.error.message : fork.isPending ? t("sessionFork.forking") : t("sessionFork.tooltip"));
	const start = () => {
		if (unavailable || inFlight.current) return;
		inFlight.current = true;
		fork.mutate(undefined, { onSettled: () => { inFlight.current = false; } });
	};
	return { refusal, unavailable, tooltip, start, pending: fork.isPending, error: fork.error, anyTurn: options.data?.anyTurn === true };
}

export function TurnForkButton() {
	const session = useContext(ForkSessionContext);
	const turnId = useContext(ForkableTurnContext);
	const latestTurnId = useContext(LatestForkTurnContext);
	if (!session || !turnId) return null;
	return <TurnForkButtonInner session={session} turnId={turnId} latest={turnId === latestTurnId} />;
}

function TurnForkButtonInner({ session, turnId, latest }: { session: WorkspaceSession; turnId: string; latest: boolean }) {
	const { t } = useTranslation();
	const fork = useSessionFork(session, turnId);
	if (!latest && !fork.anyTurn) return null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					type="button"
					variant="quiet"
					size="icon-sm"
					onClick={fork.start}
					aria-disabled={fork.unavailable}
					aria-label={t("sessionFork.fork")}
					title={fork.refusal}
					data-testid="fork-turn"
					className={cn(fork.unavailable && "opacity-40")}
				>
					<GitFork aria-hidden="true" className={cn("size-3.5", fork.pending && "animate-pulse motion-reduce:animate-none")} />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{fork.error ? fork.error.message : fork.tooltip}</TooltipContent>
		</Tooltip>
	);
}

export function useForkSessionFromMenu() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	return useCallback(
		(session: WorkspaceSession) => {
			const client = clientForSessionHost(session.hostId);
			void (async () => {
				const { data, error } = await client.POST("/api/v1/sessions/{sessionId}/fork", {
					params: { path: { sessionId: session.id } },
					body: { clientRequestId: crypto.randomUUID() },
				});
				if (error || !data) {
					showGlobalToast(t("sessionFork.failed"), apiErrorException(error, t("sessionFork.failed")).message, "error");
					return;
				}
				void queryClient.invalidateQueries({ queryKey: ["session-fork-options"] });
				void navigate(sessionNavigateTarget(session.workspaceId, data.sessionId, session.hostId));
			})();
		},
		[navigate, queryClient, showGlobalToast, t],
	);
}

export function ForkTextCopyNotice() {
	const session = useContext(ForkSessionContext);
	if (!session) return null;
	return <ForkTextCopyNoticeInner session={session} />;
}

function ForkTextCopyNoticeInner({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const origin = useSessionForkOptions(session).data?.forkedFrom;
	if (!origin?.textHandoff) return null;
	return (
		<p className="mx-auto mb-4 max-w-md px-4 text-center text-sm text-muted-foreground" data-testid="fork-text-copy-notice">
			{t("sessionFork.textCopyNotice", { title: origin.title })}
		</p>
	);
}
