/**
 * The rich parameters of a Coder template's active version, for the
 * "Template options" section. Read only for an org whose offering includes
 * coder and only once a template is chosen; the caller narrows it further to a
 * project that runs on Coder.
 *
 * With `values` (the form's draft overrides) the parameters are evaluated
 * against them (POST .../parameters/evaluate), so a parameter the template
 * declares only for some value of another (a custom repository URL once
 * "Other" is picked) appears as soon as it applies. Edits are debounced before
 * they are evaluated, and the previous list stays on screen meanwhile. An
 * older control plane without that route (404/405) falls back to
 * GET .../parameters; one without either answers 404, which reads as "no
 * parameters" so the section stays hidden.
 */

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { CloudCpError } from "../lib/cloud-cp";
import type { CloudCpCoderTemplateParameter } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudOffering } from "./useCloudOffering";

const EVALUATE_DEBOUNCE_MS = 300;

export interface UseCoderTemplateParametersResult {
	parameters: CloudCpCoderTemplateParameter[];
	isLoading: boolean;
	/** A load failure other than the route being absent. */
	isError: boolean;
	/** The parameters shown were read for the current `values`, not an earlier draft. */
	settled: boolean;
}

/** A stable key for a values map, independent of key order. */
function valuesKey(values: Readonly<Record<string, string>> | undefined): string {
	if (!values) return "";
	return JSON.stringify(Object.keys(values).sort().map((name) => [name, values[name]]));
}

function isMissingRoute(error: unknown): boolean {
	return error instanceof CloudCpError && (error.status === 404 || error.status === 405);
}

export function useCoderTemplateParameters(
	orgId: string | undefined,
	templateId: string | undefined,
	enabled = true,
	values?: Readonly<Record<string, string>>,
): UseCoderTemplateParametersResult {
	const { client, ready, baseUrl } = useCloudCp();
	const { hasCoder } = useCloudOffering();
	const id = templateId?.trim() ?? "";
	const evaluate = values !== undefined;
	const currentKey = valuesKey(values);
	const [debounced, setDebounced] = useState(() => ({ key: currentKey, values }));
	useEffect(() => {
		if (debounced.key === currentKey) return;
		const timeout = window.setTimeout(() => setDebounced({ key: currentKey, values }), EVALUATE_DEBOUNCE_MS);
		return () => window.clearTimeout(timeout);
		// currentKey stands in for values, which may be a fresh object every render.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [currentKey, debounced.key]);
	const query = useQuery({
		queryKey: ["cloud-coder-template-parameters", baseUrl, orgId, id, evaluate ? debounced.key : null],
		enabled: ready && enabled && hasCoder && Boolean(orgId) && id !== "",
		staleTime: 5 * 60_000,
		retry: 1,
		placeholderData: evaluate ? keepPreviousData : undefined,
		queryFn: async ({ signal }): Promise<CloudCpCoderTemplateParameter[]> => {
			if (evaluate) {
				try {
					const response = await client.evaluateCoderTemplateParameters(orgId as string, id, debounced.values ?? {}, { signal });
					return response.parameters ?? [];
				} catch (error) {
					if (!isMissingRoute(error)) throw error;
				}
			}
			try {
				const response = await client.getCoderTemplateParameters(orgId as string, id, { signal });
				return response.parameters ?? [];
			} catch (error) {
				if (error instanceof CloudCpError && error.status === 404) return [];
				throw error;
			}
		},
	});
	// The last list that loaded for this template, so an evaluation that fails
	// (a value the control plane refuses) keeps the form editable instead of
	// replacing every field with an error.
	const lastLoaded = useRef<{ id: string; parameters: CloudCpCoderTemplateParameter[] } | null>(null);
	if (query.data && !query.isPlaceholderData) lastLoaded.current = { id, parameters: query.data };
	const fallback = lastLoaded.current?.id === id ? lastLoaded.current.parameters : undefined;
	return {
		parameters: query.data ?? fallback ?? [],
		isLoading: query.isLoading,
		isError: query.isError,
		settled: query.isSuccess && !query.isPlaceholderData && !query.isFetching && (!evaluate || debounced.key === currentKey),
	};
}
