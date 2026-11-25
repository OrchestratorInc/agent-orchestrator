/**
 * How long the signed-in user's Cloud sessions and their conversation content
 * are kept (GET/PUT /me/data-retention). It is each user's own setting and
 * covers every Cloud session they started, team sessions included. A change
 * is previewed first with a dry run, which counts the sessions whose content
 * the new period would remove, so the caller can confirm before any is removed.
 */

import { queryOptions, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import type { CloudCpDataRetention, CloudCpDataRetentionChange } from "../lib/cloud-cp";
import { CloudCpError } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

export function cloudDataRetentionQueryKey(baseUrl: string, userId: string) {
	return ["cloud-data-retention", baseUrl, userId] as const;
}

export interface CloudDataRetention {
	/** Cloud is on and the user is signed in to it. */
	expected: boolean;
	/** The retention; null from a control plane without it, undefined while loading or failed. */
	data: CloudCpDataRetention | null | undefined;
	isPending: boolean;
	error: unknown;
	refetch: () => void;
	/** Counts the sessions whose content `days` (null: the default) would remove, changing nothing. */
	preview: (days: number | null) => Promise<CloudCpDataRetentionChange>;
	/** Saves `days` as the user's period; null follows the default again. */
	apply: (days: number | null) => Promise<CloudCpDataRetentionChange>;
}

export function useCloudDataRetention(): CloudDataRetention {
	const queryClient = useQueryClient();
	const { client, ready, baseUrl, userId } = useCloudCp();
	// Per signed-in user: after switching accounts the previous one's period never shows.
	const expected = ready && userId !== "";

	const query = useQuery(queryOptions<CloudCpDataRetention | null>({
		queryKey: cloudDataRetentionQueryKey(baseUrl, userId),
		enabled: expected,
		retry: false,
		queryFn: async ({ signal }) => {
			try {
				return await client.getDataRetention({ signal });
			} catch (error) {
				// A control plane that predates retention (404, 405), or one whose
				// store can't keep it (501 not_implemented), has nothing to show.
				if (error instanceof CloudCpError && (error.status === 404 || error.status === 405 || error.status === 501)) return null;
				throw error;
			}
		},
	}));

	const preview = useCallback(
		(days: number | null) => client.putDataRetention({ retentionDays: days, dryRun: true }),
		[client],
	);

	const apply = useCallback(async (days: number | null) => {
		const change = await client.putDataRetention({ retentionDays: days, dryRun: false });
		const { sessionsToDelete: _count, ...retention } = change;
		queryClient.setQueryData<CloudCpDataRetention | null>(cloudDataRetentionQueryKey(baseUrl, userId), retention);
		return change;
	}, [baseUrl, client, queryClient, userId]);

	const { refetch: refetchQuery } = query;
	const refetch = useCallback(() => { void refetchQuery(); }, [refetchQuery]);

	return {
		expected,
		data: query.data,
		isPending: expected && query.isPending,
		error: query.error ?? undefined,
		refetch,
		preview,
		apply,
	};
}
