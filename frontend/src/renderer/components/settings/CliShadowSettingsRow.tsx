import { useTranslation } from "react-i18next";
import { CliShadowActions, CliShadowDetails, useCliShadowStatus, useCliShadowTitle } from "../CliShadowActions";
import { SettingsRow } from "./SettingsRow";

export function CliShadowSettingsRow() {
	const { t } = useTranslation();
	const status = useCliShadowStatus();
	const title = useCliShadowTitle(status);
	if (status?.kind !== "shadowed" || status.relation === "same") return null;
	return (
		<div className="flex w-full flex-col">
			<SettingsRow label={title} description={t("cliShadow.scope")}>
				<CliShadowActions status={status} />
			</SettingsRow>
			<CliShadowDetails status={status} className="px-(--settings-row-inset) pb-2 text-muted-foreground" />
		</div>
	);
}
