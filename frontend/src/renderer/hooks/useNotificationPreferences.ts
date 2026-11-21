import { useSyncExternalStore } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cloudInboxSupport, cloudInboxUnavailable, probeCloudInbox, subscribeCloudInboxSupport } from "../lib/cloud-notifications";
import type { CloudCpNotificationPreferences } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";

export const notificationPreferencesQueryKey = (baseUrl: string, userId: string) =>
	["cloud-notification-preferences", baseUrl.replace(/\/+$/, ""), userId] as const;

function retryPreferencesRead(failureCount: number, error: unknown): boolean {
	return !cloudInboxUnavailable(error) && failureCount < 2;
}

/**
 * The signed-in person's mention alert switches, the same in every
 * organization, and a way to change one. Read only from a control plane that
 * has mentions; `available` is true once it answered, so Settings never shows
 * a page that then goes away.
 */
export function useNotificationPreferences(enabled = true) {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const queryClient = useQueryClient();
	const support = useSyncExternalStore(subscribeCloudInboxSupport, () => cloudInboxSupport(baseUrl));
	const key = notificationPreferencesQueryKey(baseUrl, userId);
	const query = useQuery({
		queryKey: key,
		enabled: enabled && ready && userId !== "" && support === "supported",
		retry: retryPreferencesRead,
		queryFn: () => probeCloudInbox(baseUrl, () => client.getMyNotificationPreferences()),
	});
	const save = useMutation({
		mutationFn: (change: Partial<CloudCpNotificationPreferences>) => client.setMyNotificationPreferences(change),
		// The switch moves at once; the server's answer replaces it, or the old value comes back.
		onMutate: async (change) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<CloudCpNotificationPreferences>(key);
			if (previous) queryClient.setQueryData<CloudCpNotificationPreferences>(key, { ...previous, ...change });
			return { previous };
		},
		onError: (_error, _change, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
		},
		onSuccess: (preferences) => {
			queryClient.setQueryData(key, preferences);
		},
	});
	return {
		available: enabled && support === "supported" && query.isSuccess,
		preferences: query.data,
		isLoading: query.isLoading,
		isError: query.isError,
		refetch: query.refetch,
		save: save.mutateAsync,
		saving: save.isPending,
		saveError: save.isError,
	};
}
