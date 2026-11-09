import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import type { WorkflowDefinition } from "../../hooks/useWorkflows";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { WorkflowProviderSelect } from "./WorkflowProviderSelect";

type Plan = components["schemas"]["WorkflowPlanDefinition"];
type Task = components["schemas"]["PlanTaskInput"];

/** 编辑候选的任务、依赖和既有仓库范围；持久化仍由同一后端校验入口处理。 */
export function WorkflowPlanDraft({ definition, value, onChange }: { definition: WorkflowDefinition; value: Plan; onChange: (plan: Plan) => void }) {
	const { t } = useTranslation();
	const update = (id: string, patch: Partial<Task>) => onChange({ ...value, tasks: value.tasks.map(task => task.id === id ? { ...task, ...patch } : task) });
	const remove = (id: string) => onChange({ ...value, tasks: value.tasks.filter(task => task.id !== id), dependencies: value.dependencies.filter(edge => edge.fromTaskId !== id && edge.toTaskId !== id), taskRepositories: value.taskRepositories.filter(repo => repo.taskId !== id) });
	const previous = definition.tasks.filter(task => !task.planner && !(value.retireTaskIds || []).includes(task.id));
	return <div className="space-y-4">
		{value.tasks.map(task => <fieldset className="space-y-3 rounded border border-border p-3" key={task.id}>
			<legend className="text-xs">{task.id}</legend>
			<WorkflowProviderSelect value={task.profile || ""} harness={task.agentHarness || ""} onChange={selection => update(task.id, selection)} />
			<label className="block text-sm">{t("workflow.name", "标题")}<Input value={task.title} onChange={e => update(task.id, { title: e.target.value })} /></label>
			<label className="block text-sm">{t("workflow.brief", "说明")}<textarea className="min-h-20 w-full rounded border border-border bg-background p-2" value={task.brief} onChange={e => update(task.id, { brief: e.target.value })} /></label>
			<div className="grid gap-2 sm:grid-cols-2"><label className="text-sm">{t("workflow.taskKind", "任务类型")}<Select value={task.kind} onValueChange={kind => update(task.id, { kind })}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{["analysis", "development", "testing", "review", "integration", "approval"].map(kind => <SelectItem key={kind} value={kind}>{kind}</SelectItem>)}</SelectContent></Select></label><label className="text-sm">{t("workflow.harness", "Agent")}<Input value={task.agentHarness} onChange={e => update(task.id, { agentHarness: e.target.value })} /></label><label className="text-sm">{t("workflow.model", "模型")}<Input disabled={!!task.profile} value={task.model} onChange={e => update(task.id, { model: e.target.value })} /></label><label className="text-sm">{t("workflow.effort", "effort")}<Input disabled={!!task.profile} value={task.effort} onChange={e => update(task.id, { effort: e.target.value })} /></label></div>
			<fieldset className="space-y-1"><legend className="text-sm">{t("workflow.dependencies", "依赖")}</legend>{[...previous, ...value.tasks].filter(parent => parent.id !== task.id).map(parent => <label className="flex gap-2 text-xs" key={parent.id}><input type="checkbox" checked={value.dependencies.some(edge => edge.fromTaskId === parent.id && edge.toTaskId === task.id)} onChange={e => onChange({ ...value, dependencies: e.target.checked ? [...value.dependencies, { fromTaskId: parent.id, toTaskId: task.id }] : value.dependencies.filter(edge => edge.fromTaskId !== parent.id || edge.toTaskId !== task.id) })} />{parent.title}</label>)}</fieldset>
			<fieldset className="space-y-2"><legend className="text-sm">{t("workflow.repositories", "选定仓库与基线")}</legend>{definition.repositories.map(repo => {
				const selected = value.taskRepositories.find(scope => scope.taskId === task.id && scope.repoKey === repo.repoKey);
				return <div key={repo.repoKey}><label className="flex gap-2 text-xs"><input type="checkbox" checked={!!selected} onChange={e => onChange({ ...value, taskRepositories: e.target.checked ? [...value.taskRepositories, { taskId: task.id, repoKey: repo.repoKey, baseSha: repo.baseSha, allowedPaths: [] }] : value.taskRepositories.filter(scope => scope !== selected) })} />{repo.repoKey} · {repo.baseSha}</label>{selected && <label className="block text-xs">{t("workflow.allowedPaths", "允许路径（每行一个；留空表示整个已选仓库）")}<textarea className="w-full rounded border border-border bg-background p-2" value={(selected.allowedPaths || []).join("\n")} onChange={e => onChange({ ...value, taskRepositories: value.taskRepositories.map(scope => scope === selected ? { ...scope, allowedPaths: e.target.value.split("\n") } : scope) })} /></label>}</div>;
			})}</fieldset>
			<Button variant="ghost" size="sm" onClick={() => remove(task.id)}>{t("workflow.removeCandidateTask", "移除候选任务")}</Button>
		</fieldset>)}
		<Button variant="outline" size="sm" onClick={() => onChange({ ...value, tasks: [...value.tasks, { id: `task-${crypto.randomUUID()}`, title: "", brief: "", kind: "development", agentHarness: "codex", model: "", effort: "", maxAttempts: 1, priority: 0 }] })}>{t("workflow.addCandidateTask", "添加候选任务")}</Button>
		<fieldset className="space-y-1"><legend className="text-sm">{t("workflow.retire", "停用旧任务")}</legend>{definition.tasks.filter(task => !task.planner && task.state !== "succeeded" && task.state !== "cancelled").map(task => <label className="flex gap-2 text-xs" key={task.id}><input type="checkbox" checked={(value.retireTaskIds || []).includes(task.id)} onChange={e => onChange({ ...value, retireTaskIds: e.target.checked ? [...(value.retireTaskIds || []), task.id] : (value.retireTaskIds || []).filter(id => id !== task.id) })} />{task.title} · {task.state}</label>)}</fieldset>
	</div>;
}
