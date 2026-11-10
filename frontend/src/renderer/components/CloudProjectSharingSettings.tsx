import { Share2 } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useTeamsEnabled } from "../hooks/useTeamsEnabled";
import { useCloudProjectsQuery } from "../hooks/useWorkspaceQuery";
import { SettingsRow } from "./settings/SettingsRow";
import { SettingsSection } from "./settings/SettingsSection";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { Button } from "./ui/button";

/**
 * Project settings › Sharing: where a Cloud project is shared as a whole.
 * Share opens the same dialog a session's "Share the whole project instead"
 * switches to, with the same people and teams, access levels, General access
 * and Copy link; it just has no session to go back to.
 */
export function CloudProjectSharingSettings({ projectId, cloudOrgId }: { projectId: string; cloudOrgId: string }) {
	const { t } = useTranslation();
	const teams = useTeamsEnabled();
	const [open, setOpen] = useState(false);
	const projectName = useCloudProjectsQuery().data?.find((project) => project.id === projectId)?.displayName;
	return (
		<>
			<SettingsSection title={t("settings.project.sharing")} titleHidden grouped sectionId="project-sharing">
				<SettingsRow label={t("settings.project.shareProject")} description={t("share.projectIncludes")}>
					<Button type="button" size="sm" variant="secondary" onClick={() => setOpen(true)} data-project-share-button="">
						<Share2 className="size-icon-md" aria-hidden="true" />
						{t("share.button")}
					</Button>
				</SettingsRow>
			</SettingsSection>
			<ShareSessionDialog
				open={open}
				onOpenChange={setOpen}
				orgId={cloudOrgId}
				projectId={projectId}
				projectName={projectName}
				people={teams}
			/>
		</>
	);
}
