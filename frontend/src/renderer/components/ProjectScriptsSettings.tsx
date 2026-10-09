import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { clearTerminateSessionState } from "../hooks/useTerminateSession";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { Button } from "./ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

type Project = components["schemas"]["Project"];
type ScriptKind = "setup" | "cleanup";

export function ProjectScriptsSettings({ projectId, onSaveState }: { projectId: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const query = useQuery({
		queryKey: ["project", projectId],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error(t("settings.project.degraded"));
			return data.project as Project;
		},
	});
	if (query.isLoading) return <p className="text-sm text-settings-muted">{t("settings.project.loading")}</p>;
	if (query.isError || !query.data) return <p role="alert" className="text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p>;
	return <ScriptsEditor key={projectId} project={query.data} onSaveState={onSaveState} onSaved={() => {
		void client.invalidateQueries({ queryKey: ["project", projectId] });
		void client.invalidateQueries({ queryKey: workspaceQueryKey });
	}} />;
}

function ScriptsEditor({ project, onSaveState, onSaved }: { project: Project; onSaveState?: (state: ProjectSettingsSaveState) => void; onSaved: () => void }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const scratch = project.kind === "scratch";
	const [activeTab, setActiveTab] = useState<ScriptKind>("setup");
	const [drafts, setDrafts] = useState<Record<ScriptKind, string[]>>(() => ({
		setup: project.config?.postCreate?.length ? [...project.config.postCreate] : [""],
		cleanup: project.config?.preRemove?.length ? [...project.config.preRemove] : [""],
	}));
	const commands = {
		postCreate: drafts.setup.filter((step) => step.trim()),
		...(!scratch && { preRemove: drafts.cleanup.filter((step) => step.trim()) }),
	};
	const [saved, setSaved] = useState(() => JSON.stringify(commands));
	const [savedAt, setSavedAt] = useState(false);
	const dirty = JSON.stringify(commands) !== saved;
	const mutation = useMutation({
		mutationFn: async (scripts: typeof commands) => {
			const current = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: project.id } } });
			if (current.error) throw new Error(apiErrorMessage(current.error));
			if (current.data?.status !== "ok" || !current.data.project) throw new Error(t("settings.project.degraded"));
			const latest = current.data.project as Project;
			const { error } = await apiClient.PUT("/api/v1/projects/{id}", {
				params: { path: { id: project.id } },
				body: { displayName: latest.name, config: { ...latest.config, ...scripts } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return scripts;
		},
		onSuccess: (scripts) => { setSaved(JSON.stringify(scripts)); setSavedAt(true); onSaved(); },
	});
	const retry = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/cleanup", { params: { query: { project: project.id } } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: (result) => {
			for (const sessionId of [...result.cleaned, ...result.alreadyGone]) clearTerminateSessionState(client, sessionId);
			onSaved();
		},
	});
	useEffect(() => {
		onSaveState?.({
			phase: mutation.isError ? "failed" : mutation.isPending ? "saving" : dirty ? "pending" : savedAt ? "saved" : "idle",
			dirty,
			requestPending: mutation.isPending,
			error: mutation.error instanceof Error ? mutation.error.message : undefined,
		});
	}, [dirty, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt]);
	const updateSteps = (kind: ScriptKind, steps: string[]) => {
		setDrafts((current) => ({ ...current, [kind]: steps }));
		setSavedAt(false);
	};
	return <form id="project-settings-form" className="flex min-h-full shrink-0 flex-col gap-3" onSubmit={(event) => { event.preventDefault(); mutation.mutate(commands); }}>
		<Tabs className="flex flex-1 flex-col gap-3" value={activeTab} onValueChange={(value) => setActiveTab(value as ScriptKind)}>
			<TabsList aria-label={t("settings.project.scripts")} className="shrink-0 self-start">
				<TabsTrigger value="setup">{t("settings.project.scriptSetup")}</TabsTrigger>
				<TabsTrigger value="cleanup">{t("settings.project.scriptCleanup")}</TabsTrigger>
			</TabsList>
			{(["setup", "cleanup"] as const).map((kind) => <TabsContent className="space-y-4" key={kind} value={kind}>
				{kind === "cleanup" && scratch ? <p className="text-sm text-settings-muted">{t("settings.project.cleanupScratchUnavailable")}</p> : <>
					<div className="space-y-1 text-pretty text-xs leading-4 text-settings-muted">
						<p>{t(kind === "setup" ? scratch ? "settings.project.setupHintScratch" : "settings.project.setupHintGit" : "settings.project.cleanupHint")}</p>
						<p>{t(`settings.project.${kind}ShellHint`)}</p>
						<p>{t("settings.project.setupPlatformHint")}</p>
					</div>
					{!scratch && <details className="rounded-md border border-border p-3 text-xs text-settings-muted">
						<summary className="cursor-pointer font-medium text-settings-label">{t(`settings.project.${kind}Paths`)}</summary>
						<div className="mt-2 space-y-1">
							<p><code>{"AO_SOURCE_TREE_PATH"}</code> — {t(`settings.project.${kind}SourcePath`)}</p>
							<p><code>{"AO_WORKTREE_PATH"}</code> — {t(`settings.project.${kind}WorktreePath`)}</p>
						</div>
					</details>}
					<div className="space-y-4">
						{drafts[kind].map((step, index) => <div key={index}>
							<div className="mb-1 flex items-center justify-between">
								<label className="text-sm font-medium text-settings-label" htmlFor={`${kind}-step-${index}`}>{t(`settings.project.${kind}Step`, { number: index + 1 })}</label>
								<button aria-label={t(kind === "setup" ? "settings.project.removeSetupStep" : "settings.project.removeCleanupStep", { number: index + 1 })} className="rounded p-1 text-settings-muted hover:text-error focus-visible:ring-2 focus-visible:ring-ring" disabled={mutation.isPending || retry.isPending} onClick={() => updateSteps(kind, drafts[kind].length === 1 ? [""] : drafts[kind].filter((_, i) => i !== index))} type="button"><Trash2 aria-hidden="true" size={16} /></button>
							</div>
							<textarea className="settings-field-control min-h-24 w-full px-4 py-3 font-mono text-xs" disabled={mutation.isPending || retry.isPending} id={`${kind}-step-${index}`} spellCheck={false} value={step} onChange={(event) => updateSteps(kind, drafts[kind].map((item, i) => i === index ? event.target.value : item))} />
						</div>)}
						<Button className="gap-1.5" disabled={mutation.isPending || retry.isPending} onClick={() => updateSteps(kind, [...drafts[kind], ""])} size="sm" type="button" variant="outline"><Plus aria-hidden="true" size={16} />{t(kind === "setup" ? "settings.project.addSetupStep" : "settings.project.addCleanupStep")}</Button>
					</div>
					{kind === "cleanup" && <div className="space-y-2 border-t border-border pt-4">
						<p className="text-xs text-settings-muted">{t("settings.project.cleanupRetryHint")}</p>
						<Button disabled={dirty || mutation.isPending || retry.isPending} onClick={() => retry.mutate()} type="button" variant="outline">{t("settings.project.retryCleanup")}</Button>
						{retry.isError && <p role="alert" className="text-sm text-error">{retry.error instanceof Error ? retry.error.message : t("settings.project.cleanupRetryFailed")}</p>}
						{retry.data && <div role="status" className="space-y-1 text-sm text-settings-muted">
							<p>{t("settings.project.cleanupRetryResult", { cleaned: retry.data.cleaned.length, skipped: retry.data.skipped.length })}</p>
							{retry.data.skipped.map((item) => <p key={item.sessionId}>{item.sessionId}: {item.reason}</p>)}
						</div>}
					</div>}
				</>}
			</TabsContent>)}
		</Tabs>
		{mutation.isError && <p role="alert" className="text-sm text-error">{mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed")}</p>}
		<div className="sticky bottom-0 z-chrome mt-auto flex items-center justify-between gap-3 border-t border-border bg-card py-3">
			<span aria-live="polite" className="min-w-0 truncate text-xs text-settings-muted">{dirty ? t("settings.project.unsavedChanges") : savedAt ? t("settings.project.saved") : ""}</span>
			<Button disabled={!dirty || mutation.isPending || retry.isPending} type="submit">{t("settings.project.saveChanges")}</Button>
		</div>
	</form>;
}
