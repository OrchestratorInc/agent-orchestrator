import { useNavigate } from "@tanstack/react-router";
import { Grid2X2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { TopbarButton } from "./TopbarButton";

export function OpenGridButton({ projectId, hostId }: { projectId: string; hostId?: string }) {
	const navigate = useNavigate();
	const { t } = useTranslation();
	return (
		<TopbarButton
			aria-label={t("grid.open")}
			className="topbar-control--labeled"
			data-priority="secondary"
			variant="feature"
			onClick={() => void navigate(hostId
				? { to: "/host/$hostId/project/$projectId/grid", params: { hostId, projectId } }
				: { to: "/projects/$projectId/grid", params: { projectId } })}
		>
			<Grid2X2 className="size-icon-md" aria-hidden="true" />
			<span data-compact-label>{t("grid.open")}</span>
		</TopbarButton>
	);
}
