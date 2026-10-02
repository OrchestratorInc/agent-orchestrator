import { useQueries } from "@tanstack/react-query";
import { apiErrorMessage } from "../lib/api-client";
import { clientForHost, labelForHost } from "../lib/host-clients";
import { useConnectedHosts } from "./useHostConnection";
import { NOTIFICATION_PAGE_SIZE, type NotificationListStatus, type NotificationsPage } from "../lib/notifications";

export const remoteNotificationsQueryKey = (hostId: string, status: NotificationListStatus) =>
	["remote-notifications", hostId, status] as const;

export async function fetchRemoteNotificationsPage(hostId: string, status: NotificationListStatus, cursor = ""): Promise<NotificationsPage> {
	const { data, error } = await clientForHost(hostId).GET("/api/v1/notifications", {
		params: { query: { status, limit: NOTIFICATION_PAGE_SIZE, cursor: cursor || undefined } },
	});
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not load notifications"));
	return data;
}

export function useRemoteNotificationHosts(status: NotificationListStatus, enabled = true) {
	const connected = useConnectedHosts();
	const queries = useQueries({
		queries: connected.map((hostId) => ({
			queryKey: remoteNotificationsQueryKey(hostId, status),
			queryFn: () => fetchRemoteNotificationsPage(hostId, status),
			enabled,
			retry: 1,
			refetchInterval: 2_000,
		})),
	});
	return {
		hosts: connected.map((hostId, index) => ({
			hostId,
			label: labelForHost(hostId) ?? hostId,
			data: queries[index]?.isError ? undefined : queries[index]?.data,
			isError: queries[index]?.isError ?? false,
			isLoading: queries[index]?.isLoading ?? false,
		})),
		totalUnreadCount: status === "unread"
			? queries.reduce((count, query) => count + (query.isError ? 0 : query.data?.unreadCount ?? 0), 0)
			: 0,
	};
}
