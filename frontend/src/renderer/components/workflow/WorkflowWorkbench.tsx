import { useState, useSyncExternalStore } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useWorkflow, useWorkflowCollaboration, useWorkflowCoordination, useWorkflows, useWorkflowScope, useWorkflowScheduler } from "../../hooks/useWorkflows";
import { aoBridge } from "../../lib/bridge";
import { getEventsConnectionState, subscribeEventsConnection } from "../../lib/events-connection";
import { useWorkflowDraft, useWorkflowUi } from "../../stores/workflow-ui-store";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../ui/tabs";
import { PageTopbar } from "../PageTopbar";
import { Button } from "../ui/button";
import { WorkflowAction } from "./WorkflowAction";
import { WorkflowGraph } from "./WorkflowGraph";
import { WorkflowPlanEditor, WorkflowCreate } from "./WorkflowPlanEditor";
import { WorkflowTaskPanel } from "./WorkflowTaskPanel";
import { WorkflowQualityPanel } from "./WorkflowQualityPanel";
import { WorkflowBudgetPanel } from "./WorkflowBudgetPanel";
import { WorkflowStartupSteps, useStartupStep } from "./WorkflowStartupSteps";

/** 工作台覆盖原路由，原 Session/Terminal 宿主保持挂载；所有业务事实来自同一 daemon。 */
export default function WorkflowWorkbench() {
	const { t } = useTranslation();
	const [cursor, setCursor] = useState<string>();
	const [creating, setCreating] = useState(false);
	const list = useWorkflows(cursor);
	const ui = useWorkflowUi();
	const scope = useWorkflowScope();
	const connection = useSyncExternalStore(subscribeEventsConnection, getEventsConnectionState);
	return <section className="flex min-h-0 flex-1 flex-col" aria-label={t("workflow.title")}>
		<PageTopbar lead={<h1 className="text-sm font-medium">{t("workflow.title")}</h1>} actions={<><Button variant="ghost" size="sm" onClick={() => ui.setOpen(false)}>{t("workflow.back")}</Button><Button size="sm" onClick={() => setCreating(true)}>{t("workflow.new")}</Button></>} />
		{connection !== "connected" && <p role="status" className="px-4 py-2 text-sm text-amber-600">{t("workflow.offline")}</p>}
		{!window.ao && <p role="status" className="px-4 py-2 text-sm">{t("workflow.browser")}</p>}
		<div className="flex min-h-0 flex-1">
			<nav aria-label={t("workflow.title")} className="w-56 shrink-0 overflow-auto border-r border-border p-2">
				{list.isPending && <p role="status">{t("workflow.loading")}</p>}
				{list.error && <p role="alert">{list.error.message}</p>}
				{list.data?.workflows.length === 0 && <p className="p-2 text-sm text-muted-foreground">{t("workflow.empty")}</p>}
				{list.data?.workflows.map(workflow => <button type="button" key={workflow.id} aria-current={!creating && ui.workflowId === workflow.id ? "page" : undefined} onClick={() => { ui.selectWorkflow(workflow.id); setCreating(false); }} className={`mb-1 w-full rounded-md px-3 py-2 text-left text-sm hover:bg-accent ${ui.workflowId === workflow.id && !creating ? "bg-accent" : ""}`}><span className="block truncate">{workflow.title}</span><span className="text-xs text-muted-foreground">{workflow.state} · r{workflow.revision}</span></button>)}
				<div className="flex gap-1"><Button variant="ghost" size="sm" disabled={!cursor} onClick={() => setCursor(undefined)}>{t("workflow.first")}</Button><Button variant="ghost" size="sm" disabled={!list.data?.nextCursor} onClick={() => setCursor(list.data?.nextCursor)}>{t("workflow.next")}</Button></div>
			</nav>
			<main className="min-w-0 flex-1 overflow-auto p-5">
				{creating ? <WorkflowCreate onCreated={id => { ui.selectWorkflow(id); setCreating(false); }} /> : ui.workflowId ? <WorkflowDetail key={`${scope[1]}:${ui.workflowId}`} id={ui.workflowId} /> : <p className="text-sm text-muted-foreground">{t("workflow.select")}</p>}
			</main>
		</div>
	</section>;
}

/** 授权动作由用户触发；SSE 失效只读取 Main 的已有只读授权，取消不会触发自动确认。 */
function WorkflowDetail({ id }: { id: string }) {
	const { t } = useTranslation();
	const definition = useWorkflow(id);
	const coordination = useWorkflowCoordination(id);
	const scheduler = useWorkflowScheduler(id);
	const collaboration = useWorkflowCollaboration(id);
	const scope = useWorkflowScope();
	const client = useQueryClient();
	const ui = useWorkflowUi();
	const [tab, setTab] = useWorkflowDraft(`${scope[1]}:${id}:tab`, "graph");
	const authorize = useMutation({ retry: false, mutationFn: () => aoBridge.operator.authorizeWorkflow(id), onSuccess: () => client.invalidateQueries({ queryKey: [...scope, id] }) });
	const step = useStartupStep(definition.data, scheduler.data?.paused);
	if (definition.isPending) return <p role="status">{t("workflow.loading")}</p>;
	if (definition.error || !definition.data) return <p role="alert">{definition.error?.message}</p>;
	const data = definition.data;
	const path = `/api/v1/workflows/${id}`;
	return <div className="space-y-5">
		<WorkflowStartupSteps step={step} />
		<div className="flex flex-wrap items-start justify-between gap-3"><div><h2 className="text-lg font-medium">{data.workflow.title}</h2><p className="mt-1 whitespace-pre-wrap text-sm text-muted-foreground">{data.workflow.goal}</p><p className="mt-2 text-xs">{data.workflow.state} · r{data.workflow.revision}</p></div>
			<div className="flex flex-wrap gap-2">
				<Button variant="outline" size="sm" disabled={!window.ao || authorize.isPending} onClick={() => authorize.mutate()}>{t("workflow.authorize")}</Button>
				{data.workflow.state === "draft" && <WorkflowAction verb="activate" path={`${path}/activate`} payload={{ expectedRevision: data.workflow.revision }}>{t("workflow.activate")}</WorkflowAction>}
				{data.workflow.state === "active" && scheduler.data && <WorkflowAction verb={scheduler.data.paused ? "resume-scheduling" : "pause"} path={`${path}/${scheduler.data.paused ? "resume-scheduling" : "pause"}`} payload={{ expectedRevision: scheduler.data.revision, reason: "用户调整工作流调度" }}>{t(scheduler.data.paused ? "workflow.resume" : "workflow.pause")}</WorkflowAction>}
			</div></div>
		{[authorize.error, coordination.error, collaboration.error].filter(Boolean).map((error, i) => <p key={i} role="alert" className="text-sm text-destructive">{error!.message}</p>)}
		<Tabs value={tab} onValueChange={setTab}>
			<TabsList aria-label={t("workflow.title")}>{(["graph", "tasks", "plan", "quality", "budget"] as const).map(value => <TabsTrigger key={value} value={value}>{t(`workflow.${value}`)}</TabsTrigger>)}</TabsList>
			<TabsContent value="plan"><WorkflowPlanEditor definition={data} /></TabsContent>
			<TabsContent value="quality">{coordination.data && <WorkflowQualityPanel id={id} quality={coordination.data.quality} tasks={coordination.data.coordination.tasks} />}</TabsContent>
			<TabsContent value="budget">{coordination.data && <WorkflowBudgetPanel id={id} taskIds={data.tasks.map(task => task.id)} data={coordination.data} />}</TabsContent>
			{["graph", "tasks"].map(value => <TabsContent key={value} value={value}>
				{value === "graph" ? <WorkflowGraph definition={data} selected={ui.taskId} onSelect={ui.selectTask} /> : <ul className="divide-y divide-border">{data.tasks.map(task => <li key={task.id}><button type="button" className="flex w-full justify-between gap-4 p-2 text-left text-sm" aria-pressed={ui.taskId === task.id} onClick={() => ui.selectTask(task.id)}><span>{task.title}</span><span>{task.state} · {task.model || task.agentHarness}</span></button></li>)}</ul>}
				{ui.taskId && <WorkflowTaskPanel key={ui.taskId} definition={data} taskId={ui.taskId} coordination={coordination.data} collaboration={collaboration.data} />}
			</TabsContent>)}
		</Tabs>
	</div>;
}
