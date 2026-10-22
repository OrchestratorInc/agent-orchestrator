import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { mergeConversationPages, toSnapshot, type ConversationSendInput } from "./useConversation";

import { sessionReviewsQueryKey } from "../lib/session-reviews";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

import type { ChatModel, TurnSettings } from "../types/conversation";

type WireSnapshot = components["schemas"]["ConversationSnapshotResponse"];
const PAGE_SIZE = 200;
export const reviewerConversationQueryRoot = ["reviewer-conversation"] as const;

export function reviewerConversationQueryKey(reviewId: string, hostId?: string) {
	return hostId ? [...reviewerConversationQueryRoot, hostId, reviewId] as const : [...reviewerConversationQueryRoot, reviewId] as const;
}

export function useReviewerConversation(reviewId: string | undefined, hostId?: string) {
	const query = useInfiniteQuery({
		queryKey: reviewerConversationQueryKey(reviewId ?? "", hostId),
		enabled: Boolean(reviewId),
		initialPageParam: undefined as number | undefined,
		queryFn: async ({ pageParam }) => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/reviews/{reviewId}/conversation", {
				params: {
					path: { reviewId: reviewId as string },
					query: { beforeSequence: pageParam, limit: PAGE_SIZE },
				},
			});
			if (error) throw error;
			return toSnapshot(data as WireSnapshot);
		},
		getNextPageParam: (page) => (page.hasMoreBefore ? page.oldestSequence : undefined),
		select: (data) => mergeConversationPages(data.pages),
	});
	return {
		snapshot: query.data,
		isLoading: query.isLoading,
		error: query.error ? apiErrorMessage(query.error) : undefined,
		hasOlder: query.hasNextPage,
		isLoadingOlder: query.isFetchingNextPage,
		loadOlder: () => void query.fetchNextPage(),
	};
}

export function useReviewerConversationModels(reviewId: string, enabled: boolean, hostId?: string) {
	const query = useQuery({
		queryKey: [...reviewerConversationQueryKey(reviewId, hostId), "models"],
		enabled,
		staleTime: 5 * 60 * 1000,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/reviews/{reviewId}/conversation/models", { params: { path: { reviewId } } });
			if (error) throw error;
			return (data?.models ?? []) as ChatModel[];
		},
	});
	return { models: query.data ?? [], error: query.error ? apiErrorMessage(query.error) : undefined };
}

export function useReviewerConversationCommands(reviewId: string | undefined, hostId?: string) {
	const queryClient = useQueryClient();
	const invalidate = useCallback(async () => {
		if (reviewId)
			await queryClient.invalidateQueries({
				queryKey: reviewerConversationQueryKey(reviewId, hostId),
			});
	}, [hostId, queryClient, reviewId]);
	const resume = useMutation({
		mutationFn: async ({ workerSessionId, targetHostId }: { workerSessionId: string; targetReviewId: string; targetHostId?: string }) => {
			const { error } = await clientForSessionHost(targetHostId).POST("/api/v1/sessions/{sessionId}/reviews/restore", {
				params: { path: { sessionId: workerSessionId } },
			});
			if (error) throw error;
		},
		onSettled: async (_data, _error, { workerSessionId, targetReviewId, targetHostId }) => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: reviewerConversationQueryKey(targetReviewId, targetHostId) }),
				queryClient.invalidateQueries({ queryKey: sessionReviewsQueryKey(workerSessionId, targetHostId) }),
				queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(targetHostId) }),
			]);
		},
	});
	const chooseSettings = useMutation({
		mutationFn: async ({ settings, targetReviewId, targetHostId }: { settings: TurnSettings; targetReviewId: string; targetHostId?: string }) => {
			const { data, error } = await clientForSessionHost(targetHostId).PATCH("/api/v1/reviews/{reviewId}/conversation/settings", {
				params: { path: { reviewId: targetReviewId } }, body: settings,
			});
			if (error) throw error;
			return data;
		},
		onSuccess: async (_data, { targetReviewId, targetHostId }) => {
			await queryClient.invalidateQueries({ queryKey: reviewerConversationQueryKey(targetReviewId, targetHostId) });
		},
	});
	const send = useMutation({
		mutationFn: async (input: ConversationSendInput) => {
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/reviews/{reviewId}/conversation/messages", {
				params: { path: { reviewId: reviewId as string } },
				headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
				body: { ...input, clientMessageId: input.clientMessageId ?? crypto.randomUUID() },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});
	const resolve = useMutation({
		mutationFn: async ({ requestId, decisionId }: { requestId: string; decisionId: string }) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/reviews/{reviewId}/conversation/approvals/{requestId}/resolve", {
				params: { path: { reviewId: reviewId as string, requestId } },
				body: { decisionId },
			});
			if (error) throw error;
		},
		onSuccess: invalidate,
	});
	const resolveInput = useMutation({
		mutationFn: async ({
			requestId,
			action,
			content,
		}: {
			requestId: string;
			action: "accept" | "decline" | "cancel";
			content?: Record<string, unknown>;
		}) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/reviews/{reviewId}/conversation/inputs/{requestId}/resolve", {
				params: { path: { reviewId: reviewId as string, requestId } },
				body: { action, content },
			});
			if (error) throw error;
		},
		onSuccess: invalidate,
	});
	const interrupt = useMutation({
		mutationFn: async () => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/reviews/{reviewId}/conversation/interrupt", {
				params: { path: { reviewId: reviewId as string } },
			});
			if (error) throw error;
		},
		onSettled: invalidate,
	});
	const error = [send.error, resolve.error, resolveInput.error, interrupt.error, chooseSettings.error].find(Boolean);
	return {
		resumeAgent: (workerSessionId: string) => {
			if (reviewId) resume.mutate({ workerSessionId, targetReviewId: reviewId, targetHostId: hostId });
		},
		resumingAgent: resume.isPending && resume.variables?.targetReviewId === reviewId && resume.variables?.targetHostId === hostId,
		resumeError: resume.variables?.targetReviewId === reviewId && resume.variables?.targetHostId === hostId && resume.error ? apiErrorMessage(resume.error) : undefined,
		chooseSettings: (settings: TurnSettings) => {
			if (reviewId) chooseSettings.mutate({ settings, targetReviewId: reviewId, targetHostId: hostId });
		},
		send: (input: ConversationSendInput) => send.mutateAsync(input),
		resolve: (requestId: string, decisionId: string) => resolve.mutate({ requestId, decisionId }),
		resolveInput: (requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>) =>
			resolveInput.mutateAsync({ requestId, action, content }),
		interrupt: () => interrupt.mutate(),
		busy: chooseSettings.isPending || send.isPending || resolve.isPending || resolveInput.isPending || interrupt.isPending,
		error: error ? apiErrorMessage(error) : undefined,
	};
}
