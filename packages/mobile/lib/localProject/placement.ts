import type { CloudProjectOffer } from "../cloud/useCanCreateCloudProject";
import type { ProjectPlacement } from "./LocalProjectForm.types";

/**
 * Which face the New project sheet shows. While a create runs, the face shown
 * stays, whatever changes around it (a machine disconnecting, the plan
 * loading): switching would unmount the form that owns the create, its clone
 * and its retry id. Otherwise the face asked for while it can make a project
 * (Cloud also while its plan is still being read); Local first, and Cloud
 * when no machine is connected, which says what it needs.
 */
export function placementFor({ chosen, cloudOffer, localOffered, busy, shown }: {
	chosen: ProjectPlacement | null;
	cloudOffer: CloudProjectOffer;
	localOffered: boolean;
	busy: boolean;
	/** The face on screen now; none before the first render. */
	shown: ProjectPlacement | null;
}): ProjectPlacement {
	if (busy && shown) return shown;
	if (chosen === "cloud" && cloudOffer !== "no") return "cloud";
	if (chosen === "local" && localOffered) return "local";
	return localOffered ? "local" : "cloud";
}
