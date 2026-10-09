import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { useSettings, useUpdateHarnessDefault, type HarnessDefault } from "../../hooks/useSettings";
import { Button } from "../ui/button";
import { AgentModelCombobox } from "./AgentModelCombobox";
import { formatEffortLabel } from "./EffortPicker";
import { SettingsOptionMenu } from "./SettingsOptionMenu";

export function HarnessModelDefaults({ agentId, agentLabel, hostId, catalog }: {
	agentId: string; agentLabel: string; hostId?: string; catalog?: AgentModelCatalog;
}) {
	const { t } = useTranslation();
	const { settings, isLoading, error } = useSettings(hostId);
	const mutation = useUpdateHarnessDefault(hostId);
	const [draft, setDraft] = useState<HarnessDefault>();
	const saved = settings?.harnessDefaults?.[agentId];
	const value = draft ?? saved;
	const model = value?.model ?? "";
	const effort = value?.effort ?? "";
	const selected = catalog?.models.find((item) => item.id === model);
	const efforts = agentId === "codex" || agentId === "claude-code" ? selected?.efforts ?? [] : [];
	const disabled = isLoading || Boolean(error) || mutation.saving;
	const changed = model !== (saved?.model ?? "") || effort !== (saved?.effort ?? "");
	const chooseModel = (next: string) => setDraft({ model: next, effort: next === model ? effort : "" });
	const save = async () => {
		try {
			await mutation.update({ agentId, model, effort });
			setDraft(undefined);
		} catch { /* The mutation's inline error preserves the draft for retry. */ }
	};
	return <div className="space-y-3 rounded-lg border border-(--color-border-settings-input) p-3">
		<h3 className="text-sm font-medium text-settings-label">{t("settings.harness.models.defaultTitle")}</h3>
		<p className="text-xs text-settings-muted">{t("settings.harness.models.defaultHint")}</p>
		<div className="flex flex-wrap items-center gap-2">
			<div className="min-w-0 flex-1">
				<AgentModelCombobox
					aria-label={t("settings.harness.models.defaultModel")} agentId={agentId} agentLabel={agentLabel}
					value={model} models={catalog?.models ?? []} allowCustom={catalog?.allowCustom} customModelEntry={catalog?.customModelEntry}
					disabled={disabled || !catalog} onChange={chooseModel} onCustom={chooseModel}
					triggerLabel={!model ? t("settings.harness.models.useHarnessDefault") : undefined}
					triggerClassName="w-full justify-between" menuAlign="start" showEffortInTrigger={false}
				/>
			</div>
			{efforts.length > 0 || effort ? <SettingsOptionMenu
				aria-label={t("settings.harness.models.defaultEffort")} value={effort} disabled={disabled}
				options={[
					{ value: "", label: t("settings.harness.models.useHarnessDefault") },
					...efforts.map((level) => ({ value: level, label: formatEffortLabel(level, t) })),
					...(effort && !efforts.includes(effort) ? [{ value: effort, label: effort }] : []),
				]}
				onChange={(next) => setDraft({ model, effort: next })}
			/> : null}
		</div>
		<div className="flex items-center justify-end gap-2">
			<Button type="button" size="sm" variant="ghost" disabled={disabled || (!model && !effort)} onClick={() => setDraft({ model: "", effort: "" })}>{t("settings.harness.models.resetDefault")}</Button>
			<Button type="button" size="sm" disabled={disabled || !changed || Boolean(effort && !efforts.includes(effort))} onClick={() => void save()}>{t(mutation.saving ? "settings.harness.models.savingDefault" : "settings.harness.models.saveDefault")}</Button>
		</div>
		{error || mutation.error ? <p role="alert" className="text-xs text-error">{error ?? mutation.error}</p> : null}
	</div>;
}
