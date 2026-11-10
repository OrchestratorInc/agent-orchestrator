import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { staleSavedTuning } from "../lib/stale-agent-tuning";
import { useUiStore } from "../stores/ui-store";

type Project = components["schemas"]["Project"];
type AgentConfig = { model?: string; effort?: string };
type Catalog = Parameters<typeof staleSavedTuning>[0];

/**
 * Resets a project's saved worker (or project-wide) model or effort that the
 * agent's current catalog no longer accepts, so a start never carries a
 * setting the spawn would refuse, and says once what changed. Runs only for
 * the project's own worker agent against an authoritative catalog.
 */
export function useResetStaleProjectTuning(args: {
	projectId?: string;
	hostId?: string;
	agent: string;
	agentLabel: string;
	projectAgent: string;
	config?: { agentConfig?: AgentConfig; worker?: { agentConfig?: AgentConfig } };
	catalog: Catalog;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const attempted = useRef(new Set<string>());
	const { projectId, hostId, agent, agentLabel, projectAgent, config, catalog } = args;
	const roleConfig = config?.worker?.agentConfig;
	const slot: "worker" | "project" = roleConfig?.model || roleConfig?.effort ? "worker" : "project";
	const saved = (slot === "worker" ? roleConfig : config?.agentConfig) ?? {};
	const model = saved.model ?? "";
	const effort = saved.effort ?? "";
	const stale = agent !== "" && agent === projectAgent ? staleSavedTuning(catalog, model, effort) : undefined;
	useEffect(() => {
		if (!projectId || !stale) return;
		const key = `${hostId ?? ""}|${projectId}|${slot}|${model}|${effort}`;
		if (attempted.current.has(key)) return;
		attempted.current.add(key);
		void (async () => {
			const client = hostId ? clientForHost(hostId) : apiClient;
			// Re-read before the whole-config PUT so no other change is erased.
			const current = await client.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
			if (current.error || current.data?.status !== "ok" || !current.data.project) return;
			const project = current.data.project as Project;
			const next = structuredClone(project.config ?? {}) as NonNullable<Project["config"]>;
			const target = slot === "worker" ? next.worker?.agentConfig : next.agentConfig;
			if (!target || (target.model ?? "") !== model || (target.effort ?? "") !== effort) return;
			if (stale.model) delete target.model;
			if (stale.effort) delete target.effort;
			const { error } = await client.PUT("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
				body: { displayName: project.name, config: next },
			});
			if (error) return;
			void queryClient.invalidateQueries({ queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId] });
			useUiStore.getState().showGlobalToast(
				t("newTask.staleTuningReset.title", { defaultValue: "Project model settings reset" }),
				stale.model
					? t("newTask.staleTuningReset.model", {
						defaultValue: "{{model}} is no longer available for {{agent}}, so this project now uses the {{agent}} default model and effort.",
						model,
						agent: agentLabel,
					})
					: t("newTask.staleTuningReset.effort", {
						defaultValue: "{{model}} no longer supports {{effort}} effort, so this project now uses its default effort.",
						model,
						effort,
					}),
				{ tone: "info", durationMs: 12_000 },
			);
		})();
	}, [agentLabel, effort, hostId, model, projectId, queryClient, slot, stale, t]);
}
