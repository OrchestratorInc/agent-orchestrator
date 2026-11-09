import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@aoagents/product-ui";
import type { components } from "../../api/schema";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { createCue, fetchProjectCues, projectCuesQueryKey } from "../lib/cues";
import { Button } from "./ui/button";

type Plan = components["schemas"]["ConductorimportPlan"];

const rowLabels = {
	"scripts.setup": "settings.project.conductor.setup",
	"scripts.archive": "settings.project.conductor.cleanup",
	environment_variables: "settings.project.conductor.env",
	"prompts.general": "settings.project.conductor.rules",
	".worktreeinclude": "settings.project.conductor.files",
	file_include_globs: "settings.project.conductor.files",
	files: "settings.project.conductor.files",
} as const;

export function ConductorImport({ projectId, onImported }: { projectId: string; onImported: () => void }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const [open, setOpen] = useState(false);
	const planKey = ["conductor-import", projectId];
	const plan = useQuery({
		queryKey: planKey,
		retry: false,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}/conductor", { params: { path: { id: projectId } } });
			if (error) throw apiErrorException(error);
			return data as Plan;
		},
	});
	const cues = useQuery({ queryKey: projectCuesQueryKey(projectId), queryFn: () => fetchProjectCues(projectId) });
	const newCues = (current: Plan) => current.cues.filter((cue) => !(cues.data ?? []).some((existing) => existing.name === cue.name));
	const run = useMutation({
		mutationFn: async () => {
			const fresh = (await plan.refetch({ throwOnError: true })).data as Plan;
			const { error } = await apiClient.PUT("/api/v1/projects/{id}/config", { params: { path: { id: projectId } }, body: { config: fresh.config } });
			if (error) throw apiErrorException(error);
			for (const cue of newCues(fresh)) await createCue(projectId, { name: cue.name, type: "command", command: cue.command });
		},
		onSuccess: async () => {
			await Promise.all([planKey, projectCuesQueryKey(projectId), ["project", projectId], workspaceQueryKey].map((key) => client.invalidateQueries({ queryKey: key })));
			setOpen(false);
			onImported();
		},
	});
	if (plan.isError) {
		return <ProjectSettingsSection title={t("settings.project.conductor.title")} titleHidden grouped>
			<p role="alert" className="settings-content-row text-sm text-error">{plan.error.message}</p>
		</ProjectSettingsSection>;
	}
	const data = plan.data;
	if (!data?.source || cues.isLoading || (data.added.length === 0 && newCues(data).length === 0)) return null;
	const cueNames = new Map(data.cues.map((cue) => [cue.key, cue.name]));
	const label = (key: string) => {
		const known = rowLabels[key as keyof typeof rowLabels];
		if (cueNames.has(key)) return t("settings.project.conductor.cue", { name: cueNames.get(key) });
		return known ? t(known) : key;
	};
	const rows = [...data.added.map((row) => ({ key: row.key, detail: row.detail ?? "" })), ...newCues(data).map((cue) => ({ key: cue.key, detail: cue.command }))];
	return <ProjectSettingsSection title={t("settings.project.conductor.title")} titleHidden grouped>
		<div className="settings-content-row flex items-center justify-between gap-3">
			<p className="min-w-0 text-sm text-settings-label">
				{t("settings.project.conductor.found")} <code className="text-xs text-settings-muted">{data.source}</code>
			</p>
			{!open && <Button onClick={() => setOpen(true)} size="sm" type="button" variant="secondary">{t("settings.project.conductor.review")}</Button>}
		</div>
		{open && <>
			{rows.map((row) => <div key={row.key} className="settings-content-row flex min-w-0 items-baseline gap-2 text-sm">
				<span className="shrink-0 font-medium text-settings-label">{label(row.key)}</span>
				<code className="min-w-0 truncate text-xs text-settings-muted" title={row.detail}>{row.detail}</code>
			</div>)}
			{data.warnings.map((warning) => <p key={warning.key + warning.detail} className="settings-content-row text-xs text-warning">
				{t("settings.project.conductor.warning", { target: label(warning.key), variable: warning.detail })}
			</p>)}
			{data.skipped.length > 0 && <p className="settings-content-row text-xs text-settings-muted">
				{t("settings.project.conductor.skipped", { keys: data.skipped.map((item) => item.key).join(", ") })}
			</p>}
			<div className="settings-content-row flex flex-wrap items-center gap-2">
				<Button disabled={run.isPending} onClick={() => run.mutate()} size="sm" type="button" variant="primary">{t("settings.project.conductor.import")}</Button>
				<Button disabled={run.isPending} onClick={() => setOpen(false)} size="sm" type="button" variant="quiet">{t("settings.project.conductor.cancel")}</Button>
				{run.isError && <p role="alert" className="text-sm text-error">{run.error.message}</p>}
			</div>
		</>}
	</ProjectSettingsSection>;
}
