import { useRef } from "react";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { useUiStore } from "../stores/ui-store";
import { CreateProjectFlow, type CreateProjectInput } from "./CreateProjectFlow";

/** Remote creation uses the same source, Git setup, and agent screens as local creation. */
export function RemoteAddProjectDialog({ hostId, hostLabel, connected, onCreated, onCreateStandaloneAgent, onOpenChange }: {
	hostId: string;
	hostLabel: string;
	connected: boolean;
	onCreated: (projectId: string, ready: boolean) => void;
	onCreateStandaloneAgent: () => void;
	onOpenChange: (open: boolean) => void;
}) {
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const setProjectProvisioning = useUiStore((state) => state.setProjectProvisioning);
	const setOrchestratorStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const createdProjectId = useRef("");
	const createProject = async (input: CreateProjectInput) => {
		if (!connected) throw new Error(`Connect to ${hostLabel} before adding a project.`);
		const client = clientForHost(hostId);
		const config: components["schemas"]["ProjectConfig"] = {
			...(input.defaultBranch ? { defaultBranch: input.defaultBranch } : {}),
			worker: { agent: input.workerAgent as components["schemas"]["RoleOverride"]["agent"] },
			orchestrator: { agent: input.orchestratorAgent as components["schemas"]["RoleOverride"]["agent"] },
			...(input.trackerIntake ? { trackerIntake: input.trackerIntake } : {}),
		};
		if (!createdProjectId.current) {
			const { data, error } = await client.POST("/api/v1/projects", { body: {
				path: input.path,
				asWorkspace: input.asWorkspace || undefined,
				clonePreparationId: input.clonePreparationId,
				config,
			} });
			if (error || !data?.project) throw new Error(apiErrorMessage(error, "Could not add project on this host."));
			createdProjectId.current = data.project.id;
			onCreated(data.project.id, false);
		}
		const projectId = createdProjectId.current;
		setOrchestratorStartupError(projectId, null, hostId);
		setProjectProvisioning(projectId, true, hostId);
		// As with local creation, project navigation must not wait on the agent process.
		const provisioningGuard = window.setTimeout(() => {
			setProjectProvisioning(projectId, false, hostId);
			setOrchestratorStartupError(projectId, "Project added, but orchestrator startup timed out. Try starting it again.", hostId);
		}, 120_000);
		void client.POST("/api/v1/orchestrators", { body: { projectId } }).then(({ data, error }) => {
			if (error || !data?.orchestrator?.id) throw new Error(apiErrorMessage(error, "Could not start the orchestrator."));
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(projectId, false, hostId);
			setOrchestratorStartupError(projectId, null, hostId);
			onCreated(projectId, true);
		}).catch((cause) => {
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(projectId, false, hostId);
			const message = cause instanceof Error ? cause.message : "Try starting it from project settings.";
			setOrchestratorStartupError(projectId, `Project added, but orchestrator did not start: ${message}`, hostId);
			showGlobalToast("Orchestrator did not start", message, "error");
		});
		window.setTimeout(() => onOpenChange(false), 0);
	};
	const initializeProject = async (path: string) => {
		if (!connected) throw new Error(`Connect to ${hostLabel} before initializing a repository.`);
		const { error } = await clientForHost(hostId).POST("/api/v1/projects/initialize", { body: { path } });
		if (error) throw new Error(apiErrorMessage(error));
	};
	return <CreateProjectFlow
		mode="choose"
		initialOpen
		hostId={hostId}
		hostLabel={hostLabel}
		connected={connected}
		onCreateProject={createProject}
		onCreateStandaloneAgent={onCreateStandaloneAgent}
		onInitializeProject={initializeProject}
		onDismiss={() => onOpenChange(false)}
	/>;
}
