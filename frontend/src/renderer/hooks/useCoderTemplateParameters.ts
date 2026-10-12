/**
 * The rich parameters of a Coder template's active version
 * (GET /orgs/{orgId}/coder-templates/{templateId}/parameters), for the
 * "Template options" section. Read only for an org whose offering includes
 * coder and only once a template is chosen; the caller narrows it further to a
 * project that runs on Coder. An older control plane without the route answers
 * 404, which reads as "no parameters" so the section stays hidden.
 */

import { useQuery } from "@tanstack/react-query";
import { CloudCpError } from "../lib/cloud-cp";
import type { CloudCpCoderTemplateParameter } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudOffering } from "./useCloudOffering";

export interface UseCoderTemplateParametersResult {
	parameters: CloudCpCoderTemplateParameter[];
	isLoading: boolean;
	/** A load failure other than the route being absent. */
	isError: boolean;
}

export function useCoderTemplateParameters(
	orgId: string | undefined,
	templateId: string | undefined,
	enabled = true,
): UseCoderTemplateParametersResult {
	const { client, ready, baseUrl } = useCloudCp();
	const { hasCoder } = useCloudOffering();
	const id = templateId?.trim() ?? "";
	const query = useQuery({
		queryKey: ["cloud-coder-template-parameters", baseUrl, orgId, id],
		enabled: ready && enabled && hasCoder && Boolean(orgId) && id !== "",
		staleTime: 5 * 60_000,
		retry: 1,
		queryFn: async ({ signal }): Promise<CloudCpCoderTemplateParameter[]> => {
			try {
				const response = await client.getCoderTemplateParameters(orgId as string, id, { signal });
				return response.parameters ?? [];
			} catch (error) {
				if (error instanceof CloudCpError && error.status === 404) return [];
				throw error;
			}
		},
	});
	return {
		parameters: query.data ?? [],
		isLoading: query.isLoading,
		isError: query.isError,
	};
}
