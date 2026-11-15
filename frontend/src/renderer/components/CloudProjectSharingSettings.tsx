import { useTeamsEnabled } from "../hooks/useTeamsEnabled";
import { ProjectSharePanel } from "./ShareSessionDialog";

export function CloudProjectSharingSettings({ projectId, cloudOrgId }: { projectId: string; cloudOrgId: string }) {
	const teams = useTeamsEnabled();
	return <ProjectSharePanel orgId={cloudOrgId} projectId={projectId} people={teams} />;
}
