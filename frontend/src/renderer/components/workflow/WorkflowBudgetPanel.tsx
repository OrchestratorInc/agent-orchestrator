import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { WorkflowAction } from "./WorkflowAction";

type S = components["schemas"];

/** 预算未知保持 UNKNOWN，只有明确金额才格式化为 USD。 */
export function workflowMoney(value: number | null | undefined) { return value == null ? "UNKNOWN" : `$${(value / 1e9).toFixed(4)}`; }

/** 配置上限与真实容量分别展示，预算策略仍由原服务负责准入及对账。 */
export function WorkflowBudgetPanel({ id, taskIds, data }: { id: string; taskIds: string[]; data: S["WorkflowCoordinationResponse"] }) {
	const { t } = useTranslation();
	const [scope, setScope] = useState("workflow");
	const [limit, setLimit] = useState("");
	const [reservation, setReservation] = useState("");
	const [mode, setMode] = useState("observe");
	const [period, setPeriod] = useState<"lifetime" | "monthly_utc">("lifetime");
	const [reason, setReason] = useState("");
	// Task 选择值使用独立前缀，合法 ID global/workflow 不能被解释成预算范围。
	const selectedScope = scope === "global" ? "global" : scope === "workflow" ? "workflow" : "task";
	const scopeId = selectedScope === "global" ? "" : selectedScope === "workflow" ? id : scope.slice("task:".length);
	const policy = data.budget.policies.find(p => p.scope === selectedScope && (p.scope === "global" || p.scopeId === scopeId) && p.period === period);
	useEffect(() => {
		setLimit(policy ? String(policy.limitNanos / 1e9) : "");
		setReservation(policy?.reservationNanos == null ? "" : String(policy.reservationNanos / 1e9));
		setMode(policy?.mode ?? "observe");
		setReason("");
	}, [scope, period, policy?.id, policy?.revision]);
	const budgetPath = selectedScope === "global" ? "/api/v1/scheduler/budget" : `/api/v1/workflows/${id}${selectedScope === "task" ? `/tasks/${scopeId}` : ""}/budget`;
	const amount = Number(limit) * 1e9;
	const estimate = reservation ? Number(reservation) * 1e9 : null;
	return <section className="space-y-4"><h3 className="text-sm font-medium">{t("workflow.budget", "预算与容量")}</h3>
		{data.scheduler && <div className="space-y-2"><p className="text-sm">{t("workflow.capacity", "实际容量")}: {data.scheduler.occupied} / {data.scheduler.limit} · {data.scheduler.reasonCode} · {data.scheduler.recovered ? t("workflow.recovered", "恢复完成") : t("workflow.recovering", "恢复中")}</p>{data.scheduler.policies.map(policy => <CapacityPolicy key={`${policy.id}:${policy.revision}`} policy={policy} />)}</div>}
		<div className="overflow-auto"><table className="w-full text-left text-xs"><thead><tr>{["范围", "统计质量", "费用 USD", "预留 USD", "剩余 USD", "对账"].map(label => <th key={label}>{t(`workflow.budgetColumn.${label}`, label)}</th>)}</tr></thead><tbody>{data.budget.snapshots.map(snapshot => <tr key={`${snapshot.policy.id}:${snapshot.window}`}><td>{snapshot.policy.scope}: {snapshot.policy.scopeId || "global"}</td><td>{snapshot.quality}</td><td>{workflowMoney(snapshot.costNanos)}</td><td>{workflowMoney(snapshot.reservedNanos)}</td><td>{workflowMoney(snapshot.remainingNanos)}</td><td>{snapshot.reconcilePending ? "PENDING" : "—"}</td></tr>)}</tbody></table></div>
		<div className="grid gap-3 sm:grid-cols-2"><label className="text-sm">{t("workflow.budgetScope", "预算范围")}<Select value={scope} onValueChange={setScope}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="global">{t("workflow.scopeGlobal", "全局")}</SelectItem><SelectItem value="workflow">{t("workflow.scopeWorkflow", "工作流")}</SelectItem>{taskIds.map(id => <SelectItem key={id} value={`task:${id}`}>{t("workflow.scopeTask", { id })}</SelectItem>)}</SelectContent></Select></label><label className="text-sm">{t("workflow.budgetMode", "控制模式")}<Select value={mode} onValueChange={v => setMode(v as typeof mode)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{["observe", "soft", "hard_admission"].map(v => <SelectItem key={v} value={v}>{v}</SelectItem>)}</SelectContent></Select></label><label className="text-sm">{t("workflow.budgetPeriod", "周期")}<Select value={period} onValueChange={v => setPeriod(v as typeof period)}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="lifetime">{t("workflow.periodLifetime", "整个周期")}</SelectItem><SelectItem value="monthly_utc">{t("workflow.periodMonthly", "每月（UTC）")}</SelectItem></SelectContent></Select></label><label className="text-sm">{t("workflow.budgetLimit", "上限 USD")}<Input type="number" min="0" value={limit} onChange={e => setLimit(e.target.value)} /></label><label className="text-sm">{t("workflow.reservation", "每次准入预留 USD")}<Input type="number" min="0" value={reservation} onChange={e => setReservation(e.target.value)} /></label><label className="text-sm">{t("workflow.reason", "修改原因")}<Input value={reason} onChange={e => setReason(e.target.value)} /></label></div>
		<WorkflowAction verb="budget" path={budgetPath} payload={{ id: policy?.id || `budget-${selectedScope}-${scopeId || "global"}-${period}`, currency: "USD", expectedRevision: policy?.revision || 0, limitNanos: amount, mode, period, reason, reservationNanos: estimate, ...(estimate !== null ? { reservationBasis: "operator_approved", reservationRef: reason } : {}) }} disabled={!limit || !Number.isSafeInteger(amount) || amount < 0 || !reason.trim() || (estimate !== null && (!Number.isSafeInteger(estimate) || estimate < 0))}>{t("workflow.saveBudget", "审批并保存预算")}</WorkflowAction>
		<p className="text-xs text-muted-foreground">{t("workflow.budgetNotice", "硬预算限制新增准入；已运行任务可能继续产生费用。UNKNOWN 不代表零费用。")}</p>
	</section>;
}

/** 修改并发只更改用户输入项，其余阈值沿用当前策略的真实快照。 */
function CapacityPolicy({ policy }: { policy: S["SchedulerPolicyResponse"] }) {
	const { t } = useTranslation();
	const [limit, setLimit] = useState(String(policy.maxConcurrent));
	const value = Number(limit);
	return <div className="flex flex-wrap items-end gap-2"><label className="text-xs">{t("workflow.configuredLimit", "配置上限")} · {(policy.scope === "adapter" ? policy.scopeId : "" ) || "global"}<Input type="number" min="1" max="10" value={limit} onChange={e => setLimit(e.target.value)} /></label><WorkflowAction verb="scheduler-policy" path="/api/v1/scheduler/policy" payload={{ harness: (policy.scope === "adapter" ? policy.scopeId : "" ), maxConcurrent: value, expectedRevision: policy.revision, cpuHardPercent: policy.cpuHardPercent, cpuRecoverPercent: policy.cpuRecoverPercent, cpuSoftPercent: policy.cpuSoftPercent, ramFloorBytes: policy.ramFloorBytes, ramHardPercent: policy.ramHardPercent, ramSoftPercent: policy.ramSoftPercent, reason: "用户调整并发上限" }} disabled={!Number.isInteger(value) || value < 1 || value > 10}>{t("workflow.saveCapacity", "审批并保存上限")}</WorkflowAction></div>;
}
