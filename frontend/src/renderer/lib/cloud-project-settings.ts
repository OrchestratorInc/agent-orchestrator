import type { CloudCpAgentProvider, CloudCpProject, CloudCpProjectAgentConfig, CloudCpProjectSettingsRequest, CloudCpProjectTrackerIntake } from "./cloud-cp";
import { CLOUD_AGENT_PROVIDERS } from "./cloud-agents";

export type CloudProjectPermissions = NonNullable<CloudCpProjectAgentConfig["permissions"]>;

export type CloudProjectRoleDraft = {
	agent: CloudCpAgentProvider | "";
	agentConfig: Required<CloudCpProjectAgentConfig>;
};

export type CloudProjectSettingsDraft = {
	displayName: string;
	defaultBranch: string;
	sessionPrefix: string;
	workerPermissions: CloudProjectPermissions;
	orchestratorPermissions: CloudProjectPermissions;
	reviewer: CloudProjectRoleDraft;
	autoReview: boolean;
	workersRequestReview: boolean;
	intake: { enabled: boolean; repo: string; assignee: string };
};

export function emptyCloudAgentConfig(): Required<CloudCpProjectAgentConfig> {
	return { model: "", mode: "", effort: "", permissions: "" };
}

function cloudAgent(value: unknown): CloudCpAgentProvider | "" {
	return CLOUD_AGENT_PROVIDERS.find((agent) => agent === value) ?? "";
}

export function cloudProjectSettingsDraft(project: CloudCpProject): CloudProjectSettingsDraft {
	const config = project.config;
	const permissions = (name: "worker" | "orchestrator"): CloudProjectPermissions => config[name]?.agentConfig?.permissions ?? "";
	const intake: CloudCpProjectTrackerIntake = config.trackerIntake ?? {};
	return {
		displayName: project.displayName,
		defaultBranch: project.defaultBranch,
		sessionPrefix: typeof config.sessionPrefix === "string" ? config.sessionPrefix : "",
		workerPermissions: permissions("worker"),
		orchestratorPermissions: permissions("orchestrator"),
		reviewer: {
			agent: cloudAgent(config.reviewers?.[0]?.harness),
			agentConfig: { ...emptyCloudAgentConfig(), ...config.reviewers?.[0]?.agentConfig },
		},
		// Like local projects, automatic review is off until the project enables it.
		autoReview: config.autoReview ?? false,
		workersRequestReview: config.workersRequestReview ?? false,
		intake: { enabled: intake.enabled ?? false, repo: intake.repo ?? "", assignee: intake.assignee ?? "" },
	};
}

// Send only changed fields. The control plane preserves every omitted setting.
export function cloudProjectSettingsPatch(before: CloudProjectSettingsDraft, after: CloudProjectSettingsDraft): CloudCpProjectSettingsRequest {
	const patch: CloudCpProjectSettingsRequest = {};
	if (before.displayName !== after.displayName) patch.displayName = after.displayName.trim();
	if (before.defaultBranch !== after.defaultBranch) patch.defaultBranch = after.defaultBranch.trim();
	const config: NonNullable<CloudCpProjectSettingsRequest["config"]> = {};
	if (before.sessionPrefix !== after.sessionPrefix) config.sessionPrefix = after.sessionPrefix.trim();
	if (before.workerPermissions !== after.workerPermissions) config.worker = { agentConfig: { permissions: after.workerPermissions } };
	if (before.orchestratorPermissions !== after.orchestratorPermissions) config.orchestrator = { agentConfig: { permissions: after.orchestratorPermissions } };
	if (JSON.stringify(before.reviewer) !== JSON.stringify(after.reviewer)) {
		config.reviewers = after.reviewer.agent === "" ? [] : [{ harness: after.reviewer.agent, agentConfig: after.reviewer.agentConfig }];
	}
	if (before.autoReview !== after.autoReview) config.autoReview = after.autoReview;
	if (before.workersRequestReview !== after.workersRequestReview) config.workersRequestReview = after.workersRequestReview;
	// The rule is sent whole, so the control plane stores exactly what the page shows.
	if (JSON.stringify(before.intake) !== JSON.stringify(after.intake)) {
		config.trackerIntake = { enabled: after.intake.enabled, repo: after.intake.repo.trim(), assignee: after.intake.assignee.trim() };
	}
	if (Object.keys(config).length > 0) patch.config = config;
	return patch;
}
