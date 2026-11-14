import { useTeamsEnabled } from "../hooks/useTeamsEnabled";
import { ProjectSharePanel } from "./ShareSessionDialog";

/**
 * Project settings › Sharing: the whole project's sharing on the page, the
 * same panel a session's "Share the whole project instead" shows in the share
 * dialog, with its people and teams, access levels, General access and Copy
 * link. Project settings is already a dialog, so it opens no second one.
 */
export function CloudProjectSharingSettings({ projectId, cloudOrgId }: { projectId: string; cloudOrgId: string }) {
	const teams = useTeamsEnabled();
	return <ProjectSharePanel orgId={cloudOrgId} projectId={projectId} people={teams} />;
}
