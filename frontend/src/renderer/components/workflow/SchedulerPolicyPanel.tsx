import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { useWorkflowScope } from "../../hooks/useWorkflows";
import { useWorkflowDraft } from "../../stores/workflow-ui-store";
import { SettingsRow } from "../settings/SettingsRow";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { WorkflowAction } from "./WorkflowAction";

type S = components["schemas"];
const GiB = 2 ** 30;

/** 草稿保留编辑开始时的版本；后台快照更新不能默默覆盖未保存字段。 */
function policyDraft(policy: S["SchedulerPolicyResponse"]) {
	return {
		expectedRevision: policy.revision,
		maxConcurrent: String(policy.maxConcurrent),
		resourceLimitsDisabled: policy.resourceLimitsDisabled,
		cpuRecoverPercent: String(policy.cpuRecoverPercent),
		cpuSoftPercent: String(policy.cpuSoftPercent),
		cpuHardPercent: String(policy.cpuHardPercent),
		ramSoftPercent: String(policy.ramSoftPercent),
		ramHardPercent: String(policy.ramHardPercent),
		ramFloorGiB: String(policy.ramFloorBytes / GiB),
	};
}

/** 一次只编辑一个策略；草稿和重放身份同时按 daemon、策略隔离。 */
export function SchedulerPolicyPanel({ status }: { status: S["SchedulerStatusResponse"] }) {
	const { t } = useTranslation();
	const scope = useWorkflowScope();
	const [selected, setSelected] = useState("global");
	const policy = status.policies.find(p => p.id === selected);
	return <div className="space-y-3">
		<p className="text-sm text-muted-foreground">{t("settings.scheduler.description")}</p>
		<p className="text-sm">{t("workflow.capacity")}: {status.occupied} / {status.limit} · {t(`settings.scheduler.status.${status.reasonCode}`, { defaultValue: t("settings.scheduler.statusUnknown", { code: status.reasonCode }) })} · {t(status.recovered ? "workflow.recovered" : "workflow.recovering")}</p>
		<p className="text-sm text-muted-foreground">{status.totalBytes > 0
			? t("settings.scheduler.memory", { available: (status.availableBytes / GiB).toFixed(2), total: (status.totalBytes / GiB).toFixed(2) })
			: t("settings.scheduler.memoryUnknown")}</p>
		<SettingsRow label={t("settings.scheduler.scope")}>
			<Select value={selected} onValueChange={setSelected}>
				<SelectTrigger aria-label={t("settings.scheduler.scope")} className="w-48"><SelectValue /></SelectTrigger>
				<SelectContent>{status.policies.map(p => <SelectItem key={p.id} value={p.id}>{p.scope === "global" ? t("workflow.scopeGlobal") : p.scopeId}</SelectItem>)}</SelectContent>
			</Select>
		</SettingsRow>
		{policy && <SchedulerPolicyEditor key={`${scope[1]}:${policy.id}`} policy={policy} />}
	</div>;
}

/** 百分比使用整数原单位；GiB 换算为安全整数 bytes 后经已有审批写入。 */
function SchedulerPolicyEditor({ policy }: { policy: S["SchedulerPolicyResponse"] }) {
	const { t } = useTranslation();
	const scope = useWorkflowScope();
	const [draft, setDraft] = useWorkflowDraft<ReturnType<typeof policyDraft> | null>(JSON.stringify([...scope, "scheduler-policy", policy.id]), null);
	const values = draft ?? policyDraft(policy);
	const [saved, setSaved] = useState(false);
	const global = policy.scope === "global";
	const payload = {
		harness: global ? "" : policy.scopeId,
		expectedRevision: values.expectedRevision,
		maxConcurrent: Number(values.maxConcurrent),
		resourceLimitsDisabled: values.resourceLimitsDisabled,
		cpuRecoverPercent: Number(values.cpuRecoverPercent),
		cpuSoftPercent: Number(values.cpuSoftPercent),
		cpuHardPercent: Number(values.cpuHardPercent),
		ramSoftPercent: Number(values.ramSoftPercent),
		ramHardPercent: Number(values.ramHardPercent),
		ramFloorBytes: Math.round(Number(values.ramFloorGiB) * GiB),
		// 审计接口只接受机器原因码，不能发送界面的中文说明。
		reason: "SCHEDULER_POLICY_UPDATE",
	} satisfies Omit<S["PutSchedulerPolicyRequest"], "idempotencyKey">;
	const percentFields = ["cpuRecoverPercent", "cpuSoftPercent", "cpuHardPercent", "ramSoftPercent", "ramHardPercent"] as const;
	const validLimit = values.maxConcurrent.trim() !== "" && Number.isInteger(payload.maxConcurrent) && payload.maxConcurrent >= 1 && payload.maxConcurrent <= 10;
	const validResources = percentFields.every(field => values[field].trim() !== "" && Number.isInteger(payload[field])) &&
		payload.cpuRecoverPercent > 0 && payload.cpuRecoverPercent < payload.cpuSoftPercent && payload.cpuSoftPercent < payload.cpuHardPercent && payload.cpuHardPercent <= 100 &&
		payload.ramHardPercent >= 0 && payload.ramHardPercent < payload.ramSoftPercent && payload.ramSoftPercent < 100 &&
		values.ramFloorGiB.trim() !== "" && Number(values.ramFloorGiB) >= 0 && Number.isSafeInteger(payload.ramFloorBytes);
	const error = !validLimit ? t("settings.scheduler.invalidConcurrency") : global && !validResources ? t("settings.scheduler.invalidResources") : null;
	const stale = draft !== null && draft.expectedRevision !== policy.revision;
	const change = (next: Partial<typeof values>) => { setDraft({ ...values, ...next }); setSaved(false); };
	return <div className="space-y-3">
		<div className="settings-grouped-rows">
			<SettingsRow label={t("settings.scheduler.maxConcurrent")} description={global ? t("settings.scheduler.globalConcurrency") : t("settings.scheduler.adapterConcurrency")}>
				<Input aria-label={t("settings.scheduler.maxConcurrent")} aria-invalid={!validLimit} className="w-24" type="number" min="1" max="10" step="1" value={values.maxConcurrent} onChange={e => change({ maxConcurrent: e.target.value })} />
			</SettingsRow>
			{global && <>
				<SettingsRow label={t("settings.scheduler.resourceLimits")} description={t("settings.scheduler.resourceLimitsDescription")}>
					<Switch aria-label={t("settings.scheduler.resourceLimits")} checked={!values.resourceLimitsDisabled} onCheckedChange={checked => change({ resourceLimitsDisabled: !checked })} />
				</SettingsRow>
				{percentFields.map(field => <SettingsRow key={field} label={t(`settings.scheduler.${field}`)}>
					<Input aria-label={t(`settings.scheduler.${field}`)} className="w-24" type="number" min={field === "ramHardPercent" ? "0" : "1"} max={field.startsWith("ram") ? "99" : "100"} step="1" value={values[field]} onChange={e => change({ [field]: e.target.value })} />
				</SettingsRow>)}
				<SettingsRow label={t("settings.scheduler.ramFloorGiB")}>
					<Input aria-label={t("settings.scheduler.ramFloorGiB")} className="w-24" type="number" min="0" step="any" value={values.ramFloorGiB} onChange={e => change({ ramFloorGiB: e.target.value })} />
				</SettingsRow>
			</>}
		</div>
		{error && <p role="alert" className="text-sm text-destructive">{error}</p>}
		{stale && <p role="status" className="text-sm text-muted-foreground">{t("settings.scheduler.conflict")}</p>}
		<div className="flex flex-wrap items-start gap-2">
			<WorkflowAction verb="scheduler-policy" path="/api/v1/scheduler/policy" requestKey={policy.id} payload={payload} disabled={!draft || !!error || stale} onSuccess={() => { setDraft(null); setSaved(true); }}>
				{t("settings.scheduler.save")}
			</WorkflowAction>
			{draft && <Button variant="ghost" size="sm" onClick={() => { setDraft(null); setSaved(false); }}>{t("settings.scheduler.discard")}</Button>}
		</div>
		{saved && <p role="status" className="text-sm text-muted-foreground">{t("settings.scheduler.saved")}</p>}
	</div>;
}
