import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { Button } from "./ui/button";

type Row = { name: string; value: string; visible: boolean };
type Project = components["schemas"]["Project"];

const rowsFromEnv = (env?: Record<string, string>): Row[] =>
	Object.entries(env ?? {}).map(([name, value]) => ({ name, value, visible: false }));

export function ProjectEnvironmentSettings({ projectId, onSaveState }: { projectId: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
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
	return <VariablesEditor key={projectId} projectId={projectId} initial={query.data.config?.env} onSaveState={onSaveState} onSaved={() => {
		void queryClient.invalidateQueries({ queryKey: ["project", projectId] });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	}} />;
}

function VariablesEditor({ projectId, initial, onSaveState, onSaved }: {
	projectId: string;
	initial?: Record<string, string>;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
	onSaved: () => void;
}) {
	const { t } = useTranslation();
	const [rows, setRows] = useState(() => rowsFromEnv(initial));
	const [saved, setSaved] = useState(() => JSON.stringify(initial ?? {}));
	const [error, setError] = useState<string | null>(null);
	const [savedAt, setSavedAt] = useState(false);
	const dirty = JSON.stringify(Object.fromEntries(rows.map(({ name, value }) => [name, value]))) !== saved;
	const mutation = useMutation({
		mutationFn: async (env: Record<string, string>) => {
			// Refresh before the whole-config PUT so this page cannot erase changes made elsewhere.
			const current = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
			if (current.error) throw new Error(apiErrorMessage(current.error));
			if (current.data?.status !== "ok" || !current.data.project) throw new Error(t("settings.project.degraded"));
			const project = current.data.project as Project;
			const { error: updateError } = await apiClient.PUT("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
				body: { displayName: project.name, config: { ...project.config, env } },
			});
			if (updateError) throw new Error(apiErrorMessage(updateError));
			return env;
		},
		onSuccess: (env) => {
			setSaved(JSON.stringify(env));
			setSavedAt(true);
			onSaved();
		},
	});
	useEffect(() => {
		onSaveState?.({
			phase: error || mutation.isError ? "failed" : mutation.isPending ? "saving" : dirty ? "pending" : savedAt ? "saved" : "idle",
			dirty,
			requestPending: mutation.isPending,
			error: error ?? (mutation.error instanceof Error ? mutation.error.message : undefined),
		});
	}, [dirty, error, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt]);
	const update = (next: Row[]) => { setRows(next); setError(null); setSavedAt(false); };
	const save = () => {
		const env: Record<string, string> = {};
		const names = new Set<string>();
		for (const row of rows) {
			const name = row.name;
			const folded = name.toUpperCase();
			if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || names.has(folded)) {
				setError(t("settings.project.envInvalid"));
				return;
			}
			names.add(folded);
			env[name] = row.value;
		}
		setError(null);
		mutation.mutate(env);
	};
	return <form id="project-settings-form" className="space-y-5 pb-6" onSubmit={(event) => { event.preventDefault(); save(); }}>
		<div>
			<h2 className="text-base font-semibold text-settings-label">{t("settings.project.environment")}</h2>
			<p className="mt-2 text-sm text-settings-muted">{t("settings.project.environmentHint")}</p>
		</div>
		<div className="space-y-3">
			{rows.map((row, index) => <div className="flex flex-wrap items-center gap-2" key={index}>
				<input aria-label={`${t("settings.project.envName")} ${index + 1}`} className="settings-field-control min-w-32 flex-1" placeholder={t("settings.project.envName")} value={row.name} onChange={(event) => update(rows.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} />
				<input aria-label={`${t("settings.project.envValue")} ${index + 1}`} autoComplete="off" className="settings-field-control min-w-32 flex-1" placeholder={t("settings.project.envValue")} type={row.visible ? "text" : "password"} value={row.value} onChange={(event) => update(rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} />
				<button aria-label={row.visible ? t("settings.project.hideVariable") : t("settings.project.showVariable")} className="rounded p-2 text-settings-muted hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring" onClick={() => update(rows.map((item, i) => i === index ? { ...item, visible: !item.visible } : item))} type="button">{row.visible ? <EyeOff size={16} /> : <Eye size={16} />}</button>
				<button aria-label={t("settings.project.removeVariable", { name: row.name || index + 1 })} className="rounded p-2 text-settings-muted hover:text-error focus-visible:ring-2 focus-visible:ring-ring" onClick={() => update(rows.filter((_, i) => i !== index))} type="button"><Trash2 size={16} /></button>
			</div>)}
			<button className="flex items-center gap-1 text-sm text-settings-label underline" onClick={() => update([...rows, { name: "", value: "", visible: false }])} type="button"><Plus size={16} />{t("settings.project.addVariable")}</button>
		</div>
		{error && <p role="alert" className="text-sm text-error">{error}</p>}
		{mutation.isError && <p role="alert" className="text-sm text-error">{mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed")}</p>}
		<div className="flex justify-end"><Button disabled={!dirty || mutation.isPending} type="submit">{t("settings.project.saveChanges")}</Button></div>
	</form>;
}
