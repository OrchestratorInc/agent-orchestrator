import { useTranslation } from "react-i18next";
import { useSchedulerStatus } from "../../hooks/useWorkflows";
import { Button } from "../ui/button";
import { SchedulerPolicyPanel } from "../workflow/SchedulerPolicyPanel";
import { SettingsSection } from "./SettingsSection";

/** 设置页只读取开放快照；所有策略写入由共用面板走现有 Operator 审批。 */
export function SchedulerSettingsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const query = useSchedulerStatus();
	return <SettingsSection sectionId="scheduler" title={t("settings.scheduler.title")} titleHidden={titleHidden}>
		{query.isPending && <p role="status" className="text-sm text-muted-foreground">{t("common.loading")}</p>}
		{query.error && <div className="space-y-2"><p role="alert" className="text-sm text-destructive">{query.error.message}</p><Button variant="outline" size="sm" onClick={() => void query.refetch()}>{t("settings.scheduler.reload")}</Button></div>}
		{query.data && <SchedulerPolicyPanel status={query.data} />}
	</SettingsSection>;
}
