import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cloudInboxUnavailable, recordCloudInboxSupport } from "../lib/cloud-notifications";
import type { CloudCpNotificationPreference, CloudCpNotificationPreferenceInput } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudInboxSupport } from "./useCloudNotifications";

export const notificationPreferencesQueryKey = (baseUrl: string, userId: string) =>
	["cloud-notification-preferences", baseUrl.replace(/\/+$/, ""), userId] as const;

function retryPreferencesRead(failureCount: number, error: unknown): boolean {
	return !cloudInboxUnavailable(error) && failureCount < 2;
}

/**
 * The signed-in person's notification choices, one per kind, the same in
 * every organization; and a way to change some of them. `available` is true
 * only once the control plane answered: one from before team notifications
 * has no preferences, so Settings shows no Notifications page there.
 */
export function useNotificationPreferences(enabled = true) {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const queryClient = useQueryClient();
	const support = useCloudInboxSupport(baseUrl);
	const key = notificationPreferencesQueryKey(baseUrl, userId);
	const query = useQuery({
		queryKey: key,
		enabled: enabled && ready && userId !== "" && support !== "unsupported",
		retry: retryPreferencesRead,
		queryFn: async () => {
			try {
				return (await client.getNotificationPreferences()).items;
			} catch (error) {
				if (cloudInboxUnavailable(error)) recordCloudInboxSupport(baseUrl, "unsupported");
				throw error;
			}
		},
	});
	const save = useMutation({
		mutationFn: async (items: CloudCpNotificationPreferenceInput[]) => (await client.setNotificationPreferences(items)).items,
		// The change shows at once; the server's answer (locked kinds kept on) replaces it.
		onMutate: async (items) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<CloudCpNotificationPreference[]>(key);
			if (previous) {
				queryClient.setQueryData<CloudCpNotificationPreference[]>(key, previous.map((preference) => {
					const change = items.find((item) => item.kind === preference.kind);
					return change ? { ...preference, ...change, inApp: preference.locked || change.inApp, scope: change.scope ?? preference.scope } : preference;
				}));
			}
			return { previous };
		},
		onError: (_error, _items, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
		},
		onSuccess: (items) => {
			queryClient.setQueryData(key, items);
		},
	});
	return {
		available: enabled && support !== "unsupported" && query.isSuccess,
		preferences: query.data,
		isLoading: query.isLoading,
		isError: query.isError,
		refetch: query.refetch,
		save: save.mutateAsync,
		saving: save.isPending,
		saveError: save.isError,
	};
}
