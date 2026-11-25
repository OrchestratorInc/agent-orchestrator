import { useTranslation } from "react-i18next";
import { type IdleCompactionMode, useSettings, useUpdateIdleCompaction } from "../../../hooks/useSettings";
import { SettingsOptionMenu } from "../SettingsOptionMenu";
import { HarnessDetailGroup, HarnessDetailRow } from "./harness-ui";

/** When an idle Claude Chat compacts its history. Claude's prompt cache
 * expires after an hour idle, and the next message then reprocesses the whole
 * history at full price; compacting first keeps that message small. */
export function ClaudeIdleCompactionGroup() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateIdleCompaction();
	const options: { value: IdleCompactionMode; label: string }[] = [
		{ value: "onReturn", label: t("settings.harness.idleCompaction.onReturn") },
		{ value: "beforeCacheExpires", label: t("settings.harness.idleCompaction.beforeCacheExpires") },
		{ value: "off", label: t("settings.harness.idleCompaction.off") },
	];
	return <HarnessDetailGroup title={t("settings.harness.idleCompaction.group")}>
		<HarnessDetailRow label={t("settings.harness.idleCompaction.label")} description={t("settings.harness.idleCompaction.description")}>
			<SettingsOptionMenu
				aria-label={t("settings.harness.idleCompaction.label")}
				value={settings?.idleCompaction ?? "onReturn"}
				options={options}
				onChange={update}
				disabled={isLoading || saving || !settings}
				triggerClassName="h-8 w-fit"
			/>
		</HarnessDetailRow>
		{error ? <p role="alert" className="px-3 pb-2 text-xs text-error">{error}</p> : null}
	</HarnessDetailGroup>;
}
