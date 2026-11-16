/**
 * AO Cloud automations: scheduled prompts a cloud project's worker runs in the
 * control plane, shared with the organization. Offered when the active
 * organization's offering lists the automations feature; every hook here is
 * idle otherwise.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { CloudCpError, type CloudCpAutomation, type CloudCpAutomationRun, type CloudCpCreateAutomationRequest, type CloudCpUpdateAutomationRequest } from "../lib/cloud-cp";
import { AUTOMATIONS_FEATURE } from "../lib/cloud-offering";
import { useCloudCp } from "./useCloudCp";
import { useCloudMeUserId } from "./useCloudMeUserId";
import { useCloudOffering } from "./useCloudOffering";

export type CloudAutomation = CloudCpAutomation;
export type CloudAutomationRun = CloudCpAutomationRun;

/** Harnesses a cloud automation can run: those with a Chat controller, whose opening turn ends the run. */
export const CLOUD_AUTOMATION_HARNESSES = ["claude-code", "codex", "cursor"] as const;

export const cloudAutomationsQueryKey = ["cloud-automations"] as const;

const IN_FLIGHT_RUN_STATUSES: ReadonlySet<string> = new Set(["pending", "starting", "running"]);

/** Whether a run is still going: it may change on the control plane's next tick. */
export function isCloudRunInFlight(run: Pick<CloudAutomationRun, "status"> | undefined): boolean {
	return Boolean(run && IN_FLIGHT_RUN_STATUSES.has(run.status));
}

// The control plane's automation loop ticks every 30s (and at once for Run now),
// so a run in flight is followed every 15s; otherwise the list only changes at a
// scheduled time or by someone's edit, and once a minute is enough.
const IN_FLIGHT_POLL_MS = 15_000;
const IDLE_POLL_MS = 60_000;

/** How often the automation list refreshes: closely while any automation's latest run is in flight. */
export function cloudAutomationPollInterval(automations: ReadonlyArray<Pick<CloudAutomation, "latestRun">> | undefined): number {
	return automations?.some((item) => isCloudRunInFlight(item.latestRun)) ? IN_FLIGHT_POLL_MS : IDLE_POLL_MS;
}

/** How often an expanded run history refreshes: closely while one of its runs is in flight. */
export function cloudRunsPollInterval(runs: ReadonlyArray<Pick<CloudAutomationRun, "status">> | undefined): number {
	return runs?.some((run) => isCloudRunInFlight(run)) ? IN_FLIGHT_POLL_MS : IDLE_POLL_MS;
}

/** Whether cloud automations are offered, and the organization and query key they live under. */
export function useCloudAutomationsScope() {
	const { client, baseUrl } = useCloudCp();
	const offering = useCloudOffering();
	const orgId = offering.org?.id ?? "";
	const available = offering.ready && orgId !== "" && offering.features.includes(AUTOMATIONS_FEATURE);
	return { client, orgId, available, queryKey: [...cloudAutomationsQueryKey, baseUrl, orgId] as const };
}

export function useCloudAutomations() {
	const { client, orgId, available, queryKey } = useCloudAutomationsScope();
	// runAs and createdByUserId are control-plane user ids (GET /me user.id),
	// not the desktop's sign-in account id, which is the identity provider's.
	const userId = useCloudMeUserId();
	const query = useQuery({
		queryKey,
		enabled: available,
		queryFn: async ({ signal }) => {
			const automations: CloudAutomation[] = [];
			let cursor: string | undefined;
			do {
				const page = await client.listAutomations(orgId, { limit: 100, cursor }, { signal });
				automations.push(...page.items);
				cursor = page.page.hasMore ? page.page.nextCursor : undefined;
			} while (cursor);
			return automations;
		},
		refetchInterval: (current) => cloudAutomationPollInterval(current.state.data),
	});
	/** The signed-in Cloud user's control-plane id, undefined until known: an edit by anyone else makes the automation run as the editor. */
	return { ...query, available, orgId, userId };
}

/** Replaces one automation in the cached list, or adds it. */
function useCacheAutomation() {
	const queryClient = useQueryClient();
	const { queryKey } = useCloudAutomationsScope();
	return (automation: CloudAutomation) => queryClient.setQueryData<CloudAutomation[]>(queryKey, (current = []) =>
		current.some((item) => item.id === automation.id) ? current.map((item) => item.id === automation.id ? automation : item) : [automation, ...current]);
}

export function useCreateCloudAutomation() {
	const { client, orgId } = useCloudAutomationsScope();
	const cache = useCacheAutomation();
	return useMutation({
		mutationFn: async (body: CloudCpCreateAutomationRequest) => (await client.createAutomation(orgId, body)).automation,
		onSuccess: cache,
	});
}

export function useUpdateCloudAutomation() {
	const { client, orgId } = useCloudAutomationsScope();
	const cache = useCacheAutomation();
	return useMutation({
		mutationFn: async ({ id, body }: { id: string; body: CloudCpUpdateAutomationRequest }) => (await client.updateAutomation(orgId, id, body)).automation,
		onSuccess: cache,
	});
}

export function useDeleteCloudAutomation() {
	const queryClient = useQueryClient();
	const { client, orgId, queryKey } = useCloudAutomationsScope();
	return useMutation({
		mutationFn: async (id: string) => {
			await client.deleteAutomation(orgId, id);
			return id;
		},
		onSuccess: (id) => queryClient.setQueryData<CloudAutomation[]>(queryKey, (current = []) => current.filter((item) => item.id !== id)),
	});
}

/** Runs an automation now, as the signed-in person; the list refreshes to show the run. */
export function useRunCloudAutomationNow() {
	const queryClient = useQueryClient();
	const { client, orgId, queryKey } = useCloudAutomationsScope();
	return useMutation({
		mutationFn: async (id: string) => (await client.runAutomationNow(orgId, id)).run,
		onSuccess: (run) => {
			void queryClient.invalidateQueries({ queryKey });
			void queryClient.invalidateQueries({ queryKey: [...queryKey, run.automationId, "runs"] });
		},
	});
}

export function useSubscribeCloudAutomation() {
	const queryClient = useQueryClient();
	const { client, orgId, queryKey } = useCloudAutomationsScope();
	return useMutation({
		mutationFn: async ({ id, subscribed }: { id: string; subscribed: boolean }) => {
			await client.setAutomationSubscription(orgId, id, subscribed);
			return { id, subscribed };
		},
		onSuccess: ({ id, subscribed }) => queryClient.setQueryData<CloudAutomation[]>(queryKey, (current = []) =>
			current.map((item) => item.id === id ? { ...item, subscribed } : item)),
	});
}

export function useCloudAutomationRuns(id: string | null) {
	const { client, orgId, available, queryKey } = useCloudAutomationsScope();
	return useQuery({
		queryKey: [...queryKey, id, "runs"],
		enabled: available && Boolean(id),
		queryFn: async ({ signal }) => (id ? (await client.listAutomationRuns(orgId, id, { limit: 100 }, { signal })).items : []),
		refetchInterval: (current) => cloudRunsPollInterval(current.state.data),
	});
}

const ERROR_CODES = [
	"agent_provider_required",
	"AUTOMATION_LIMIT_REACHED",
	"AUTOMATION_RUN_ACTIVE",
	"AUTOMATION_EDIT_FORBIDDEN",
	"AUTOMATION_PROJECT_UNSUPPORTED",
] as const;

/** A control-plane refusal as the person reads it; the server's own message for anything else. */
export function cloudAutomationErrorMessage(error: unknown, t: TFunction): string | null {
	if (!error) return null;
	if (error instanceof CloudCpError && error.code && (ERROR_CODES as readonly string[]).includes(error.code)) {
		return t(`automations.cloud.error.${error.code as (typeof ERROR_CODES)[number]}`);
	}
	return error instanceof Error ? error.message : String(error);
}

const RUN_ERROR_CODES = [
	"PREVIOUS_RUN_ACTIVE",
	"AUTOMATION_DISABLED",
	"SANDBOX_QUOTA_EXCEEDED",
	"TIMED_OUT",
	"AGENT_FAILED",
	"SESSION_ENDED",
	"START_FAILED",
	"agent_provider_required",
] as const;

/** The plan refusals a run is skipped with (the contract's AutomationRunStatus): one message for all. */
const PLAN_LIMIT_CODES: ReadonlySet<string> = new Set([
	"SESSION_LIMIT_REACHED",
	"WORKER_SLOTS_FULL",
	"ORCHESTRATOR_SLOTS_FULL",
	"WINDOW_LIMIT_REACHED",
	"WEEKLY_LIMIT_REACHED",
]);

/** Why a cloud run did not finish, as the person reads it; the server's words for a code this app does not know. */
export function cloudRunErrorMessage(run: Pick<CloudAutomationRun, "errorCode" | "errorMessage">, t: TFunction): string | undefined {
	if (run.errorCode && (RUN_ERROR_CODES as readonly string[]).includes(run.errorCode)) {
		return t(`automations.cloud.runError.${run.errorCode as (typeof RUN_ERROR_CODES)[number]}`);
	}
	if (run.errorCode && PLAN_LIMIT_CODES.has(run.errorCode)) return t("automations.cloud.runError.PLAN_LIMIT");
	return run.errorMessage || undefined;
}
