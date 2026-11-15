import { useQuery } from "@tanstack/react-query";
import type { CloudCpCoderTemplate } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudOffering } from "./useCloudOffering";

export interface UseCoderTemplatesResult {
	templates: CloudCpCoderTemplate[];
	isLoading: boolean;
	isError: boolean;
}

export function useCoderTemplates(orgId: string | undefined, enabled: boolean): UseCoderTemplatesResult {
	const { client, ready, baseUrl } = useCloudCp();
	const { hasCoder } = useCloudOffering();
	const query = useQuery({
		queryKey: ["cloud-coder-templates", baseUrl, orgId],
		enabled: ready && enabled && hasCoder && Boolean(orgId),
		staleTime: 5 * 60_000,
		retry: 1,
		queryFn: async (): Promise<CloudCpCoderTemplate[]> => {
			const response = await client.listCoderTemplates(orgId as string);
			return response.templates ?? [];
		},
	});
	return {
		templates: query.data ?? [],
		isLoading: query.isLoading,
		isError: query.isError,
	};
}
