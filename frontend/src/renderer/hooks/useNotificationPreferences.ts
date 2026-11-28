import { useSyncExternalStore } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cloudInboxSupport, cloudInboxUnavailable, probeCloudInbox, subscribeCloudInboxSupport } from "../lib/cloud-notifications";
import type { CloudCpNotificationPreferences } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useMentionsOffered } from "./useMentionsOffered";

export const notificationPreferencesQueryKey = (baseUrl: string, userId: string) =>
	["cloud-notification-preferences", baseUrl.replace(/\/+$/, ""), userId] as const;

function retryPreferencesRead(failureCount: number, error: unknown): boolean {
	return !cloudInboxUnavailable(error) && failureCount < 2;
}

/**
 * The signed-in person's mention alert switches, the same in every
 * organization, and a way to change one. Read only from a control plane that
 * has mentions and offers them (MENTIONS_FEATURE); `available` is true once it
 * answered, so Settings never shows a page that then goes away.
 */
export function useNotificationPreferences(enabled = true) {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const offered = useMentionsOffered(enabled && ready);
	const queryClient = useQueryClient();
	const support = useSyncExternalStore(subscribeCloudInboxSupport, () => cloudInboxSupport(baseUrl));
	const key = notificationPreferencesQueryKey(baseUrl, userId);
	const query = useQuery({
		queryKey: key,
		enabled: enabled && ready && offered && userId !== "" && support === "supported",
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
		// A failed save puts back only the switches it changed, and only where they
		// still show its value: another save may have moved them since, or moved
		// the others (a whole-object rollback would undo those).
		onError: (_error, change, context) => {
			const before = context?.previous;
			if (!before) return;
			queryClient.setQueryData<CloudCpNotificationPreferences>(key, (current) => {
				if (!current) return before;
				const restored = { ...current };
				for (const name of Object.keys(change) as Array<keyof CloudCpNotificationPreferences>) {
					if (current[name] === change[name] && before[name] !== undefined) Object.assign(restored, { [name]: before[name] });
				}
				return restored;
			});
		},
		onSuccess: (preferences) => {
			queryClient.setQueryData(key, preferences);
		},
		// Then the switches read what the server holds.
		onSettled: () => void queryClient.invalidateQueries({ queryKey: key }),
	});
	return {
		available: enabled && offered && support === "supported" && query.isSuccess,
		preferences: query.data,
		isLoading: query.isLoading,
		isError: query.isError,
		refetch: query.refetch,
		save: save.mutateAsync,
		saving: save.isPending,
		saveError: save.isError,
	};
}
