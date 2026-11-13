import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { sessionScmSummaryQueryKey, type SessionPRSummary } from "./useSessionScmSummary";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

export type PREditContent = components["schemas"]["PREditContentResponse"];
export type PREditField = "title" | "body";

export const prEditContentQueryKey = (url: string, hostId?: string) => ["pr-edit-content", hostId ?? "local", url] as const;

/** A save the provider refused because the text changed there after the editor read it. */
export class PREditConflict extends Error {}

/**
 * Editing a PR's title and description. `access` says, before anything is
 * clicked, whether the signed-in account can edit the PR at all; `load` reads
 * the title and whole description from the provider as an editor opens, since
 * the stored description is cut to a view-sized cap and can be a poll behind;
 * `save` changes one field, and throws PREditConflict when the provider no
 * longer holds the text the editor started from.
 */
export function usePREdit(pr: SessionPRSummary | undefined, sessionId: string, hostId: string | undefined, enabled: boolean) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const queryKey = prEditContentQueryKey(pr?.url ?? "", hostId);
	const fetchContent = async (): Promise<PREditContent> => {
		if (!pr) throw new Error(t("pr.edit.loadFailed"));
		const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/prs/{id}/edit", {
			params: { path: { id: String(pr.number) }, query: { prUrl: pr.url } },
		});
		if (error || !data) throw apiErrorException(error, t("pr.edit.loadFailed"));
		return data;
	};
	const access = useQuery({
		queryKey,
		queryFn: fetchContent,
		// A merged or closed PR is not edited here, so there is nothing to ask.
		enabled: enabled && !usePreviewData && Boolean(pr?.url) && (pr?.state === "open" || pr?.state === "draft"),
		staleTime: 10 * 60_000,
		retry: false,
	});
	// Always asks the provider: an editor must start from what it holds now.
	const load = () => queryClient.fetchQuery({ queryKey, queryFn: fetchContent, staleTime: 0, retry: false });

	const save = useMutation({
		mutationFn: async ({ base, field, value }: { base: string; field: PREditField; value: string }) => {
			if (!pr) throw new Error(t("pr.edit.saveFailed"));
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/edit", {
				params: { path: { id: String(pr.number) } },
				body: field === "title" ? { prUrl: pr.url, title: value, baseTitle: base } : { prUrl: pr.url, body: value, baseBody: base },
			});
			if (error || !data) {
				if (apiErrorCode(error) === "PR_EDIT_CONFLICT") throw new PREditConflict(t("pr.edit.conflict"));
				if (apiErrorCode(error) === "PR_EDIT_FORBIDDEN") throw new Error(t("pr.edit.blocked.noPermission"), { cause: error });
				throw apiErrorException(error, t("pr.edit.saveFailed"));
			}
			return data;
		},
		onSuccess: (result, { field }) => {
			// Shown at once; the refetch reconciles the rest.
			queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: unknown[] }>(
				sessionScmSummaryQueryKey(sessionId, hostId),
				(current) => current ? {
					...current,
					prs: current.prs.map((cached) => cached.url !== pr?.url ? cached
						: field === "title" ? { ...cached, title: result.title }
							: { ...cached, description: result.body, descriptionTruncated: false }),
				} : current,
			);
			queryClient.setQueryData<PREditContent>(queryKey, (current) => current ? { ...current, title: result.title, body: result.body } : current);
			void queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId, hostId) });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		},
		onError: (error) => {
			// The daemon stored what the provider holds now: show it behind the editor.
			if (error instanceof PREditConflict) void queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId, hostId) });
		},
	});
	return { access, load, save };
}
