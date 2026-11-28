import { useApp } from "../store";
import { useCloud } from "./CloudProvider";
import { cloudAgentsOffered } from "./plan";
import { useCloudPlan } from "./planStore";

/** Whether a new cloud project can be made, or the answer is still on its way. */
export type CloudProjectOffer = "yes" | "no" | "pending";

/**
 * Whether a new cloud project could run anything: signed in to Orchestrator
 * Cloud, on a plan that runs cloud agents (a Free organization runs none).
 * Pending while the account or the plan is still being read, so a caller can
 * keep an explicit choice of Cloud instead of treating it as unavailable.
 */
export function useCloudProjectOffer(): CloudProjectOffer {
	const { cloud } = useApp();
	const { status, client, org } = useCloud();
	const plan = useCloudPlan(client, cloud ? org?.id : undefined);
	if (!cloud) return status === "loading" || status === "connecting" ? "pending" : "no";
	const offered = cloudAgentsOffered(plan, org?.offering);
	return offered === undefined ? "pending" : offered ? "yes" : "no";
}

export function useCanCreateCloudProject(): boolean {
	return useCloudProjectOffer() === "yes";
}
