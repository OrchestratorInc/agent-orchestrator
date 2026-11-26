/**
 * AO Cloud automations: scheduled prompts a cloud project's worker runs in the
 * control plane, shared with the organization. Offered when the active
 * organization's offering lists the automations feature; every hook here is
 * idle otherwise.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { CloudCpError, type CloudCpAutomation, type CloudCpAutomationRun, type CloudCpClient, type CloudCpCreateAutomationRequest, type CloudCpUpdateAutomationRequest } from "../lib/cloud-cp";
import { agentLabel } from "../lib/agent-options";
import { AUTOMATIONS_FEATURE } from "../lib/cloud-offering";
import { useCloudCp } from "./useCloudCp";
import { useCloudMeUserId } from "./useCloudMeUserId";
import { useCloudOffering } from "./useCloudOffering";

export type CloudAutomationRun = CloudCpAutomationRun;
/**
 * A cloud automation as this app lists it: the contract's, plus the run in
 * flight that a newer skipped occurrence hides from latestRun (the contract's
 * latestRun is the newest occurrence, and an occurrence skipped while a run
 * goes is newer than that run). The list finds it in the run history.
 */
export type CloudAutomation = CloudCpAutomation & { hiddenInFlightRun?: CloudAutomationRun };

/** Harnesses a cloud automation can run: those with a Chat controller, whose opening turn ends the run. */
export const CLOUD_AUTOMATION_HARNESSES = ["claude-code", "codex", "cursor"] as const;

export const cloudAutomationsQueryKey = ["cloud-automations"] as const;

const IN_FLIGHT_RUN_STATUSES: ReadonlySet<string> = new Set(["pending", "starting", "running"]);

/** Whether a run is still going: it may change on the control plane's next tick. */
export function isCloudRunInFlight(run: Pick<CloudAutomationRun, "status"> | undefined): boolean {
	return Boolean(run && IN_FLIGHT_RUN_STATUSES.has(run.status));
}

/** The automation's run in flight, if any: its latest run, or one a newer skipped occurrence hides. */
export function cloudAutomationActiveRun(item: Pick<CloudAutomation, "latestRun" | "hiddenInFlightRun">): CloudAutomationRun | undefined {
	if (isCloudRunInFlight(item.latestRun)) return item.latestRun;
	return isCloudRunInFlight(item.hiddenInFlightRun) ? item.hiddenInFlightRun : undefined;
}

// The control plane's automation loop ticks every 30s (and at once for Run now),
// so a run in flight is followed every 15s; otherwise the list only changes at a
// scheduled time or by someone's edit, and once a minute is enough.
const IN_FLIGHT_POLL_MS = 15_000;
const IDLE_POLL_MS = 60_000;

/** How often the automation list refreshes: closely while any automation has a run in flight. */
export function cloudAutomationPollInterval(automations: ReadonlyArray<Pick<CloudAutomation, "latestRun" | "hiddenInFlightRun">> | undefined): number {
	return automations?.some((item) => cloudAutomationActiveRun(item)) ? IN_FLIGHT_POLL_MS : IDLE_POLL_MS;
}

/** How often an expanded run history refreshes: closely while one of its runs is in flight. */
export function cloudRunsPollInterval(runs: ReadonlyArray<Pick<CloudAutomationRun, "status">> | undefined): number {
	return runs?.some((run) => isCloudRunInFlight(run)) ? IN_FLIGHT_POLL_MS : IDLE_POLL_MS;
}

// A run in flight stops at its automation's maxRuntimeMinutes: the control
// plane times it out on a tick after that. Slack for those ticks and for a
// start that was still being retried when the occurrence was skipped.
const RUN_STOP_SLACK_MS = 15 * 60_000;

/**
 * Whether the latest run is a skipped occurrence that may hide a run still in
 * flight: one going when it was skipped, which stops at its time limit, so a
 * skip older than that hides none and costs no lookup.
 */
function mayHideInFlightRun(item: Pick<CloudAutomation, "latestRun" | "maxRuntimeMinutes">, now: number): boolean {
	const latest = item.latestRun;
	if (latest?.status !== "skipped") return false;
	const skippedAt = Date.parse(latest.finishedAt ?? latest.scheduledFor);
	return Number.isFinite(skippedAt) && now - skippedAt < item.maxRuntimeMinutes * 60_000 + RUN_STOP_SLACK_MS;
}

// A run lasts at most 480 minutes and occurrences are at least an hour apart,
// so at most nine skipped occurrences are newer than a run in flight.
const HIDDEN_RUN_LOOKUP_LIMIT = 20;

/**
 * The run in flight behind an automation's newer skipped occurrences, from its
 * run history (newest occurrence first). Only one run goes at a time and a
 * skipped occurrence never started, so the newest run that was not skipped is
 * the only one that can still be going.
 */
async function findHiddenInFlightRun(client: CloudCpClient, orgId: string, automationId: string, signal: AbortSignal): Promise<CloudAutomationRun | undefined> {
	const runs = await client.listAutomationRuns(orgId, automationId, { limit: HIDDEN_RUN_LOOKUP_LIMIT }, { signal });
	const newestStarted = runs.items.find((run) => run.status !== "skipped");
	return isCloudRunInFlight(newestStarted) ? newestStarted : undefined;
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
			const now = Date.now();
			return Promise.all(automations.map(async (item): Promise<CloudAutomation> => {
				if (!mayHideInFlightRun(item, now)) return item;
				try {
					const hiddenInFlightRun = await findHiddenInFlightRun(client, orgId, item.id, signal);
					return hiddenInFlightRun ? { ...item, hiddenInFlightRun } : item;
				} catch (error) {
					if (signal.aborted) throw error;
					// The row still shows what the list says; the next refresh looks again.
					return item;
				}
			}));
		},
		refetchInterval: (current) => cloudAutomationPollInterval(current.state.data),
	});
	/** The signed-in Cloud user's control-plane id, undefined until known: an edit by anyone else makes the automation run as the editor. */
	return { ...query, available, orgId, userId };
}

/**
 * An automation the control plane just returned, keeping the run in flight the
 * list found behind its skipped latest run while that skip is still its latest.
 */
function refreshedAutomation(cached: CloudAutomation | undefined, automation: CloudAutomation): CloudAutomation {
	if (!cached?.hiddenInFlightRun || automation.hiddenInFlightRun || automation.latestRun?.id !== cached.latestRun?.id) return automation;
	return { ...automation, hiddenInFlightRun: cached.hiddenInFlightRun };
}

/** Replaces one automation in the cached list, or adds it. */
function useCacheAutomation() {
	const queryClient = useQueryClient();
	const { queryKey } = useCloudAutomationsScope();
	return (automation: CloudAutomation) => queryClient.setQueryData<CloudAutomation[]>(queryKey, (current = []) =>
		current.some((item) => item.id === automation.id)
			? current.map((item) => item.id === automation.id ? refreshedAutomation(item, automation) : item)
			: [automation, ...current]);
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

/**
 * Runs an automation now, as the signed-in person. The queued run is its latest
 * at once, so the row shows it in flight (and Run now stays off) before the
 * list refreshes; a refusal because a run is already going refreshes the list
 * to show that run.
 */
export function useRunCloudAutomationNow() {
	const queryClient = useQueryClient();
	const { client, orgId, queryKey } = useCloudAutomationsScope();
	return useMutation({
		mutationFn: async (id: string) => (await client.runAutomationNow(orgId, id)).run,
		onSuccess: (run) => {
			queryClient.setQueryData<CloudAutomation[]>(queryKey, (current) => current?.map((item) => item.id === run.automationId
				? { ...item, latestRun: run, hiddenInFlightRun: undefined }
				: item));
			void queryClient.invalidateQueries({ queryKey });
			void queryClient.invalidateQueries({ queryKey: [...queryKey, run.automationId, "runs"] });
		},
		onError: (error, id) => {
			if (!(error instanceof CloudCpError) || error.code !== "AUTOMATION_RUN_ACTIVE") return;
			void queryClient.invalidateQueries({ queryKey });
			void queryClient.invalidateQueries({ queryKey: [...queryKey, id, "runs"] });
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
	// Saving checks what a run needs (the control plane's preflight).
	"repository_not_accessible",
	"repository_unreachable",
	"SANDBOX_QUOTA_EXCEEDED",
	"PLAN_REQUIRED",
] as const;

/**
 * A control-plane refusal as the person reads it; the server's own message for
 * anything else. harness is the agent the refused request runs with: a missing
 * login then names the agent to log in to, as the control plane's message does.
 *
 * saving is a save or a turn-on, which the control plane refuses for a full
 * workspace or plan only when its limit is zero (no run could ever start), so
 * the fix is a bigger limit or plan. Anything else (Run now) is refused for
 * being full right now, which freeing a session or waiting fixes.
 */
export function cloudAutomationErrorMessage(error: unknown, t: TFunction, { harness, saving = false }: { harness?: string; saving?: boolean } = {}): string | null {
	if (!error) return null;
	if (error instanceof CloudCpError && error.code === "agent_provider_required" && harness) {
		return t("automations.cloud.error.agentLoginRequired", { agent: agentLabel(harness) });
	}
	if (saving && error instanceof CloudCpError && error.code === "SANDBOX_QUOTA_EXCEEDED") return t("automations.cloud.error.save.SANDBOX_QUOTA_EXCEEDED");
	if (error instanceof CloudCpError && error.code && (ERROR_CODES as readonly string[]).includes(error.code)) {
		return t(`automations.cloud.error.${error.code as (typeof ERROR_CODES)[number]}`);
	}
	if (error instanceof CloudCpError && error.code && PLAN_LIMIT_CODES.has(error.code)) {
		return t(saving ? "automations.cloud.error.save.PLAN_LIMIT" : "automations.cloud.error.PLAN_LIMIT");
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
	"STOPPED",
	"agent_provider_required",
	"forbidden",
	"PLAN_REQUIRED",
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
