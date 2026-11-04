import { useTranslation } from "react-i18next";
import { useCliShadowDetail, useCliShadowStatus } from "../CliShadowBanner";

// Keeps the warning reachable after the startup banner is dismissed, and only
// appears when there is something to act on.
export function CliShadowSettingsRow() {
	const { t } = useTranslation();
	const detail = useCliShadowDetail(useCliShadowStatus());
	if (!detail) return null;
	return (
		<div className="flex w-full flex-col gap-0.5 px-3 py-2 text-xs leading-relaxed">
			<p className="font-medium text-foreground">{t("cliShadow.title")}</p>
			<p className="wrap-break-word text-muted-foreground">{detail}</p>
		</div>
	);
}
