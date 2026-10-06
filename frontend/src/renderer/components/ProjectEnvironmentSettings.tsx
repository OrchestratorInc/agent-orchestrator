import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, FileCode2, KeyRound, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { Button } from "./ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

type Row = { name: string; value: string; visible: boolean };
type Project = components["schemas"]["Project"];

const rowsFromEnv = (env?: Record<string, string>): Row[] =>
	Object.entries(env ?? {}).map(([name, value]) => ({ name, value, visible: false }));

function parsePastedEnv(text: string): { rows: Row[]; invalidLine?: number } {
	const rows: Row[] = [];
	const names = new Set<string>();
	for (const [index, source] of text.split(/\r?\n/).entries()) {
		const line = source.trim();
		if (!line || line.startsWith("#")) continue;
		const match = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
		if (!match) return { rows: [], invalidLine: index + 1 };
		const name = match[1];
		const folded = name.toUpperCase();
		if (folded.startsWith("AO_") || names.has(folded)) return { rows: [], invalidLine: index + 1 };
		names.add(folded);
		let value = match[2].trim();
		if (value.startsWith('"') || value.startsWith("'")) {
			const quoted = /^(['"])(.*)\1(?:\s*#.*)?$/.exec(value);
			if (!quoted) return { rows: [], invalidLine: index + 1 };
			value = quoted[2];
			if (quoted[1] === '"') value = value.replace(/\\n/g, "\n").replace(/\\r/g, "\r");
		} else {
			value = value.replace(/\s+#.*$/, "").trimEnd();
		}
		if (value.includes("\0")) return { rows: [], invalidLine: index + 1 };
		rows.push({ name, value, visible: false });
	}
	return { rows };
}

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
	const [activeTab, setActiveTab] = useState<"variables" | "paste">("variables");
	const [pasteText, setPasteText] = useState("");
	const [importedCount, setImportedCount] = useState<number | null>(null);
	const dirty = JSON.stringify(Object.fromEntries(rows.map(({ name, value }) => [name, value]))) !== saved;
	const variableCount = rows.filter(({ name }) => name.trim()).length;
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
			phase: mutation.isError ? "failed" : mutation.isPending ? "saving" : dirty ? "pending" : savedAt ? "saved" : "idle",
			dirty,
			requestPending: mutation.isPending,
			error: mutation.error instanceof Error ? mutation.error.message : undefined,
		});
	}, [dirty, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt]);
	const update = (next: Row[]) => { setRows(next); setError(null); setSavedAt(false); setImportedCount(null); };
	const importPasted = () => {
		const parsed = parsePastedEnv(pasteText);
		if (parsed.invalidLine || parsed.rows.length === 0) {
			setError(t("settings.project.envPasteInvalid", { line: parsed.invalidLine ?? 1 }));
			return;
		}
		const next = [...rows];
		for (const row of parsed.rows) {
			const existing = next.findIndex((item) => item.name.toUpperCase() === row.name.toUpperCase());
			if (existing < 0) next.push(row);
			else next[existing] = { ...row, name: next[existing].name };
		}
		update(next);
		setImportedCount(parsed.rows.length);
		setPasteText("");
		setActiveTab("variables");
	};
	const save = () => {
		if (activeTab === "paste" && pasteText.trim()) {
			setError(t("settings.project.envPastePending"));
			return;
		}
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
	return <form id="project-settings-form" className="flex min-h-full flex-col gap-3" onSubmit={(event) => { event.preventDefault(); save(); }}>
		<div className="flex flex-wrap items-start justify-between gap-2">
			<div className="min-w-0">
				<div className="flex flex-wrap items-center gap-2">
					<h2 className="text-sm font-semibold text-settings-label">{t("settings.project.environmentVariables")}</h2>
					<span className="rounded-full border border-border bg-muted/30 px-2 py-0.5 text-2xs font-medium text-muted-foreground">{t("settings.project.environmentCount", { count: variableCount })}</span>
				</div>
				<p className="mt-1 max-w-2xl text-xs leading-4 text-settings-muted">{t("settings.project.environmentHint")}</p>
			</div>
		</div>
		<Tabs className="flex min-h-0 flex-1 flex-col gap-3" onValueChange={(value) => { setActiveTab(value as "variables" | "paste"); setError(null); }} value={activeTab}>
			<TabsList aria-label={t("settings.project.environmentVariables")} className="shrink-0 self-start rounded-md bg-muted/30">
				<TabsTrigger value="variables">{t("settings.project.environmentVariables")}</TabsTrigger>
				<TabsTrigger value="paste"><FileCode2 aria-hidden="true" className="size-4" />{t("settings.project.pasteVariables")}</TabsTrigger>
			</TabsList>
			<TabsContent className="flex min-h-0 flex-1 flex-col gap-3" value="variables">
				<div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border border-border bg-muted/10">
					<div className="min-h-0 flex-1 overflow-y-auto">
						{rows.length > 0 ? <>
							<div className="sticky top-0 z-chrome grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] items-center gap-2 border-b border-border bg-muted/20 px-3 py-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
								<span>{t("settings.project.envName")}</span>
								<span>{t("settings.project.envValue")}</span>
								<span className="sr-only">{t("settings.project.showVariable")}</span>
								<span aria-hidden="true" className="sr-only">{t("settings.project.envValue")}</span>
							</div>
							{rows.map((row, index) => <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] items-center gap-2 border-b border-border px-3 py-2.5 last:border-b-0" key={index}>
								<input aria-label={`${t("settings.project.envName")} ${index + 1}`} className="settings-field-control min-w-0" placeholder={t("settings.project.envName")} value={row.name} onChange={(event) => update(rows.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} />
								<input aria-label={`${t("settings.project.envValue")} ${index + 1}`} autoComplete="off" className="settings-field-control min-w-0" placeholder={t("settings.project.envValue")} type={row.visible ? "text" : "password"} value={row.value} onChange={(event) => update(rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} />
								<button aria-label={row.visible ? t("settings.project.hideVariable") : t("settings.project.showVariable")} className="rounded p-2 text-settings-muted hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring" onClick={() => update(rows.map((item, i) => i === index ? { ...item, visible: !item.visible } : item))} title={row.visible ? t("settings.project.hideVariable") : t("settings.project.showVariable")} type="button">{row.visible ? <EyeOff size={16} /> : <Eye size={16} />}</button>
								<button aria-label={t("settings.project.removeVariable", { name: row.name || index + 1 })} className="rounded p-2 text-settings-muted hover:text-error focus-visible:ring-2 focus-visible:ring-ring" onClick={() => update(rows.filter((_, i) => i !== index))} title={t("settings.project.removeVariable", { name: row.name || index + 1 })} type="button"><Trash2 size={16} /></button>
							</div>)}
						</> : <div className="flex flex-col items-center px-6 py-10 text-center">
							<KeyRound aria-hidden="true" className="mb-3 size-5 text-settings-muted" />
							<p className="text-sm font-medium text-settings-label">{t("settings.project.environmentEmptyTitle")}</p>
							<p className="mt-1 max-w-sm text-sm text-settings-muted">{t("settings.project.environmentEmptyHint")}</p>
						</div>}
					</div>
					<div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-border bg-background/30 px-3 py-2.5">
						<Button className="gap-1.5" onClick={() => update([...rows, { name: "", value: "", visible: false }])} size="sm" type="button" variant="outline"><Plus aria-hidden="true" />{t("settings.project.addVariable")}</Button>
						<Button className="gap-1.5" onClick={() => { setActiveTab("paste"); setError(null); }} size="sm" type="button" variant="ghost"><FileCode2 aria-hidden="true" />{t("settings.project.pasteVariables")}</Button>
					</div>
				</div>
			</TabsContent>
			<TabsContent className="flex min-h-0 flex-1 flex-col gap-3" value="paste">
				<div className="flex min-h-0 flex-1 flex-col gap-2.5 rounded-lg border border-border bg-muted/10 p-3">
					<div className="shrink-0">
						<p className="text-sm font-medium text-settings-label">{t("settings.project.pasteVariables")}</p>
						<p className="mt-1 text-sm text-settings-muted">{t("settings.project.envPasteHint")}</p>
					</div>
					<textarea aria-label={t("settings.project.pasteVariables")} autoComplete="off" className="settings-field-control min-h-28 min-w-0 flex-1 font-mono text-sm" id="project-env-paste" onChange={(event) => setPasteText(event.target.value)} spellCheck={false} value={pasteText} />
					<div className="flex shrink-0 justify-end gap-2">
						<Button onClick={() => { setActiveTab("variables"); setPasteText(""); setError(null); }} size="sm" type="button" variant="outline">{t("settings.project.envPasteCancel")}</Button>
						<Button disabled={!pasteText.trim()} onClick={importPasted} size="sm" type="button">{t("settings.project.importVariables")}</Button>
					</div>
				</div>
			</TabsContent>
		</Tabs>
		{importedCount !== null && <p role="status" className="text-sm text-settings-muted">{t("settings.project.envImported", { count: importedCount })}</p>}
		{error && <p role="alert" className="text-sm text-error">{error}</p>}
		{mutation.isError && <p role="alert" className="text-sm text-error">{mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed")}</p>}
		<div className="sticky bottom-0 z-chrome mt-auto flex items-center justify-between gap-3 border-t border-border bg-card py-3">
			<span aria-live="polite" className="min-w-0 truncate text-xs text-settings-muted">{dirty ? t("settings.project.unsavedChanges") : savedAt ? t("settings.project.saved") : ""}</span>
			<Button disabled={!dirty || mutation.isPending} type="submit">{t("settings.project.saveChanges")}</Button>
		</div>
	</form>;
}
