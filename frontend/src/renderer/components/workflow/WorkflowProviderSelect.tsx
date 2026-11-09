import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useWorkflowScope, workflowError } from "../../hooks/useWorkflows";
import { apiClient } from "../../lib/api-client";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

/** 只选择宿主批准的固定引用；清除选择恢复原生模型输入，目录错误不替换当前值。 */
export function WorkflowProviderSelect({ value, harness, onChange }: {
	value: string; harness: string; onChange: (selection: { profile: string; model: string; effort: string }) => void;
}) {
	const { t } = useTranslation();
	const scope = useWorkflowScope();
	const profiles = useQuery({ queryKey: [...scope, "provider-profiles"], queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/workflows/provider-profiles", { signal });
		if (error) throw workflowError(error);
		return data!.profiles;
	} });
	const choices = (profiles.data || []).filter(profile => profile.harness === harness);
	const missing = !!value && !!profiles.data && !choices.some(profile => profile.reference === value);
	return <div className="space-y-1">
		<label className="block text-sm">{t("workflow.provider", "供应商配置")}<Select value={value || "__native__"} onValueChange={reference => {
			if (reference === "__native__") { onChange({ profile: "", model: "", effort: "" }); return; }
			const profile = choices.find(choice => choice.reference === reference);
			if (profile) onChange({ profile: reference, model: profile.model, effort: profile.effort || "" });
		}}><SelectTrigger aria-label={t("workflow.provider", "供应商配置")}><SelectValue /></SelectTrigger><SelectContent>
			<SelectItem value="__native__">{t("workflow.nativeProvider", "原生配置")}</SelectItem>
			{choices.map(profile => <SelectItem key={profile.reference} value={profile.reference}>{profile.name} · {profile.model}</SelectItem>)}
			{value && !choices.some(profile => profile.reference === value) && <SelectItem value={value} disabled>{t("workflow.currentProvider", { id: value.split("@")[0] })}</SelectItem>}
		</SelectContent></Select></label>
		{harness !== "codex" && <p className="text-xs text-muted-foreground">{t("workflow.nativeProviderOnly", "此 Agent 暂仅支持原生供应商配置。")}</p>}
		{profiles.error && <p role="alert" className="text-xs">{profiles.error.message}</p>}
		{missing && <p role="alert" className="text-xs">{t("workflow.providerChanged", "供应商配置已变化或与 Agent 不匹配，请重新选择或清除。")}</p>}
		{value && <p className="text-xs text-muted-foreground" title={value}>{t("workflow.fixedProvider", { id: value.split("@")[0] })}</p>}
	</div>;
}
