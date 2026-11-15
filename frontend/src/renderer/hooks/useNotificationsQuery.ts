import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
	applyNotificationsCleared,
	applyNotificationDeleted,
	applyOptimisticNotificationDelete,
	clearAllNotifications,
	deleteNotification,
	fetchNotificationsPage,
	markAllCachedNotificationsRead,
	markAllNotificationsRead,
	notificationsQueryKey,
	reconcileNotifications,
	type NotificationListStatus,
	unreadNotificationsQueryKey,
	rollbackOptimisticNotificationDelete,
	type NotificationDTO,
} from "../lib/notifications";

export function useNotificationsQuery(status: NotificationListStatus, enabled = true) {
	return useInfiniteQuery({
		queryKey: notificationsQueryKey(status),
		queryFn: ({ pageParam, signal }) => fetchNotificationsPage(status, pageParam, signal),
		initialPageParam: "",
		getNextPageParam: (lastPage) => lastPage.nextCursor || undefined,
		enabled,
		retry: 1,
	});
}

export function useMarkAllNotificationsReadMutation() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: markAllNotificationsRead,
		onSuccess: (updatedCount, ids) => {
			markAllCachedNotificationsRead(queryClient, ids, updatedCount);
			if (ids.length === 0) {
				void queryClient.invalidateQueries({ queryKey: unreadNotificationsQueryKey });
			}
		},
	});
}

export function useClearAllNotificationsMutation() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: clearAllNotifications,
		onMutate: () => queryClient.cancelQueries({ queryKey: ["notifications", "history"] }, { revert: false }),
		onSuccess: async (result) => {
			await queryClient.cancelQueries({ queryKey: ["notifications", "history"] }, { revert: false });
			applyNotificationsCleared(queryClient, result);
			await reconcileNotifications(queryClient);
		},
	});
}

export function useClearNotificationMutation() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (notification: NotificationDTO) => deleteNotification(notification),
		onMutate: async (notification) => {
			await queryClient.cancelQueries({ queryKey: ["notifications", "history"] }, { revert: false });
			applyOptimisticNotificationDelete(queryClient, notification);
		},
		onSuccess: async (notification) => {
			await queryClient.cancelQueries({ queryKey: ["notifications", "history"] }, { revert: false });
			applyNotificationDeleted(queryClient, notification);
		},
		onError: (_error, notification) => {
			rollbackOptimisticNotificationDelete(queryClient, notification.id);
		},
		onSettled: () => reconcileNotifications(queryClient),
	});
}
