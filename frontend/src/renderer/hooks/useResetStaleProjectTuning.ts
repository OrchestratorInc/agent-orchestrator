import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { staleSavedTuning } from "../lib/stale-agent-tuning";
import { useUiStore } from "../stores/ui-store";

type Project = components["schemas"]["Project"];
type AgentConfig = { model?: string; effort?: string };
type Catalog = Parameters<typeof staleSavedTuning>[0];
type TuningConfig = { agentConfig?: AgentConfig; worker?: { agentConfig?: AgentConfig } };

function effectiveSavedTuning(config?: TuningConfig) {
	const worker = config?.worker?.agentConfig;
	const modelSlot = worker?.model ? "worker" : "project";
	const effortSlot = worker?.effort ? "worker" : "project";
	return {
		model: worker?.model || config?.agentConfig?.model || "",
		effort: worker?.effort || config?.agentConfig?.effort || "",
		modelSlot,
		effortSlot,
	};
}

/** Offers an explicit reset for stale saved worker tuning; opening the composer never writes settings. */
export function useResetStaleProjectTuning(args: {
	projectId?: string;
	hostId?: string;
	agent: string;
	agentLabel: string;
	projectAgent: string;
	config?: TuningConfig;
	catalog: Catalog;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const attempted = useRef(new Set<string>());
	const latest = useRef(args);
	useLayoutEffect(() => { latest.current = args; }, [args]);
	const { projectId, hostId, agent, agentLabel, projectAgent, config, catalog } = args;
	const { model, effort, modelSlot, effortSlot } = effectiveSavedTuning(config);
	const stale = agent !== "" && agent === projectAgent ? staleSavedTuning(catalog, model, effort, agent) : undefined;
	useEffect(() => {
		if (!projectId || !stale) return;
		const key = `${hostId ?? ""}|${projectId}|${modelSlot}|${model}|${effortSlot}|${effort}`;
		if (attempted.current.has(key)) return;
		attempted.current.add(key);
		const stillCurrent = () => {
			const current = latest.current;
			if (current.projectId !== projectId || current.hostId !== hostId || current.agent !== agent || current.projectAgent !== projectAgent) return false;
			const tuning = effectiveSavedTuning(current.config);
			const invalid = staleSavedTuning(current.catalog, tuning.model, tuning.effort, current.agent);
			return tuning.model === model && tuning.effort === effort
				&& tuning.modelSlot === modelSlot && tuning.effortSlot === effortSlot
				&& invalid?.model === stale.model && invalid?.effort === stale.effort;
		};
		const reset = async () => {
			if (!stillCurrent()) return;
			const client = hostId ? clientForHost(hostId) : apiClient;
			// Re-read before the whole-config PUT so no other change is erased.
			const current = await client.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
			if (current.error) throw current.error;
			if (current.data?.status !== "ok" || !current.data.project) return;
			const project = current.data.project as Project;
			const next = structuredClone(project.config ?? {}) as NonNullable<Project["config"]>;
			const currentTuning = effectiveSavedTuning(next);
			if (currentTuning.model !== model || currentTuning.effort !== effort
				|| currentTuning.modelSlot !== modelSlot || currentTuning.effortSlot !== effortSlot) return;
			const modelTarget = modelSlot === "worker" ? next.worker?.agentConfig : next.agentConfig;
			const effortTarget = effortSlot === "worker" ? next.worker?.agentConfig : next.agentConfig;
			if (stale.model && modelTarget) delete modelTarget.model;
			if (stale.effort && effortTarget) delete effortTarget.effort;
			if (!stillCurrent()) return;
			const { error } = await client.PUT("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
				body: { displayName: project.name, config: next },
			});
			if (error) throw error;
			void queryClient.invalidateQueries({ queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId] });
			useUiStore.getState().showGlobalToast(
				t("newTask.staleTuningReset.title", { defaultValue: "Project model settings reset" }),
				undefined, { tone: "success" },
			);
		};
		let confirmed = false;
		const runReset = () => {
			if (confirmed) return;
			confirmed = true;
			void reset().catch(() => {
				confirmed = false;
				useUiStore.getState().showGlobalToast(
					t("newTask.staleTuningReset.failed", { defaultValue: "Could not reset saved model settings" }), undefined,
					{ tone: "error", durationMs: 0, dismissible: true, action: { label: t("common.retry", { defaultValue: "Retry" }), run: runReset } },
				);
			});
		};
		useUiStore.getState().showGlobalToast(
			t("newTask.staleTuningReset.proposalTitle", { defaultValue: "Saved model settings need attention" }),
			stale.model
				? t("newTask.staleTuningReset.proposalModel", {
					defaultValue: "{{model}} is not listed for {{agent}}. Reset this project's saved model and effort?",
					model, agent: agentLabel,
				})
				: t("newTask.staleTuningReset.proposalEffort", {
					defaultValue: "{{model}} does not support {{effort}} effort. Reset this project's saved effort?",
					model: model || t("settings.models.default", { defaultValue: "Agent default" }), effort,
				}),
			{
				tone: "info", durationMs: 0, dismissible: true,
				action: {
					label: t("newTask.staleTuningReset.confirm", { defaultValue: "Reset saved settings" }),
					run: runReset,
				},
			},
		);
	}, [agent, agentLabel, effort, effortSlot, hostId, model, modelSlot, projectAgent, projectId, queryClient, stale, t]);
}
