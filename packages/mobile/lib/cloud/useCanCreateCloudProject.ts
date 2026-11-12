import { useApp } from "../store";
import { useCloud } from "./CloudProvider";
import { cloudAgentsOffered } from "./plan";
import { useCloudPlan } from "./planStore";

/**
 * Whether a new cloud project could run anything: signed in to Orchestrator
 * Cloud, on a plan that runs cloud agents. A Free organization runs none.
 */
export function useCanCreateCloudProject(): boolean {
	const { cloud } = useApp();
	const { client, org } = useCloud();
	const plan = useCloudPlan(client, cloud ? org?.id : undefined);
	return Boolean(cloud) && cloudAgentsOffered(plan, org?.offering) === true;
}
