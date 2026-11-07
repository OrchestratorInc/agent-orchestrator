import type { WorkflowDefinition } from "../../hooks/useWorkflows";
import { useTranslation } from "react-i18next";

/** 服务端 DAG 的只读层级投影；节点同时提供键盘按钮与文字依赖，颜色不承担唯一语义。 */
export function WorkflowGraph({ definition, selected, onSelect }: { definition: WorkflowDefinition; selected: string; onSelect: (id: string) => void }) {
	const { t } = useTranslation();
	const levels = new Map<string, number>();
	for (let pass = 0; pass < definition.tasks.length; pass++) {
		for (const task of definition.tasks) {
			const parents = definition.edges.filter(edge => edge.toTaskId === task.id).map(edge => edge.fromTaskId);
			if (parents.every(id => levels.has(id))) levels.set(task.id, parents.length ? Math.max(...parents.map(id => levels.get(id)!)) + 1 : 0);
		}
	}
	const unresolved = Math.max(-1, ...levels.values()) + 1;
	for (const task of definition.tasks) if (!levels.has(task.id)) levels.set(task.id, unresolved);
	const columns = Array.from(new Set(levels.values())).sort((a, b) => a - b);
	return <div className="flex gap-6 overflow-auto py-3" aria-label={t("workflow.graph")}>
		{columns.map(level => <div className="flex w-56 shrink-0 flex-col gap-3" key={level}>
			{definition.tasks.filter(task => levels.get(task.id) === level).map(task => <button key={task.id} type="button" aria-pressed={selected === task.id} onClick={() => onSelect(task.id)} className={`rounded-md border p-3 text-left focus-visible:outline-2 ${selected === task.id ? "border-primary bg-accent" : "border-border"}`}>
				<span className="block text-sm font-medium">{task.title}</span><span className="block text-xs text-muted-foreground">{task.state} · {task.agentHarness || "—"}</span>
				<span className="block break-words text-xs text-muted-foreground">{definition.edges.filter(edge => edge.toTaskId === task.id).map(edge => definition.tasks.find(task => task.id === edge.fromTaskId)?.title ?? edge.fromTaskId).join(" + ") || t("workflow.rootTask", "起点")} →</span>
			</button>)}
		</div>)}
	</div>;
}
