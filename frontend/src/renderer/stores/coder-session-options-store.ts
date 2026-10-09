import { create } from "zustand";
import type { CloudCpProjectCoderConfig, CloudCpSessionRepo } from "../lib/cloud-cp/types";

export type CoderSize = "small" | "medium" | "large";

// The per-session Coder picker choices (template + curated form). Kept in memory
// only — these are per-session choices, not a persisted preference, and reset
// when the composer clears. "" templateId means the deployment default template,
// which sends no picker options and preserves the pre-existing behavior.
export interface CoderSessionOptionsState {
	templateId: string;
	// The parameter names the chosen template declares, tracked alongside the id
	// so size/startup are only ever offered and sent when the template accepts
	// them (sending an undeclared rich parameter makes Coder reject the build).
	supportedParams: string[];
	size: CoderSize;
	startupScript: string;
	extraRepos: CloudCpSessionRepo[];
	// Project-level prefix for new Coder workspace names; "" keeps the default.
	workspaceNamePrefix: string;
	setTemplate: (templateId: string, supportedParams: string[]) => void;
	setSize: (size: CoderSize) => void;
	setStartupScript: (startupScript: string) => void;
	setExtraRepos: (extraRepos: CloudCpSessionRepo[]) => void;
	setWorkspaceNamePrefix: (workspaceNamePrefix: string) => void;
	reset: () => void;
}

const initialState = {
	templateId: "",
	supportedParams: [] as string[],
	size: "medium" as CoderSize,
	startupScript: "",
	extraRepos: [] as CloudCpSessionRepo[],
	workspaceNamePrefix: "",
};

export const useCoderSessionOptionsStore = create<CoderSessionOptionsState>((set) => ({
	...initialState,
	setTemplate: (templateId, supportedParams) => set({ templateId, supportedParams }),
	setSize: (size) => set({ size }),
	setStartupScript: (startupScript) => set({ startupScript }),
	setExtraRepos: (extraRepos) => set({ extraRepos }),
	setWorkspaceNamePrefix: (workspaceNamePrefix) => set({ workspaceNamePrefix }),
	reset: () => set({ ...initialState, supportedParams: [], extraRepos: [] }),
}));

// buildCoderRequestOptions turns the picker state into the createProject `coder`
// payload, or undefined when the choice is "Default with no extra repos" — in
// which case the request omits `coder` entirely and the project behaves exactly
// as before. Size and startup are only sent alongside a chosen (non-default)
// template, mirroring the control plane's validation. A workspace name prefix
// is sent only when set (the caller validates it before create).
export function buildCoderRequestOptions(state: {
	templateId: string;
	supportedParams: string[];
	size: CoderSize;
	startupScript: string;
	extraRepos: CloudCpSessionRepo[];
	workspaceNamePrefix?: string;
}): CloudCpProjectCoderConfig | undefined {
	const templateId = state.templateId.trim();
	const extraRepos = state.extraRepos
		.map((repo) => ({ url: repo.url.trim(), branch: repo.branch?.trim() || undefined }))
		.filter((repo) => repo.url.length > 0);
	const workspaceNamePrefix = state.workspaceNamePrefix?.trim() ?? "";
	if (!templateId && extraRepos.length === 0 && !workspaceNamePrefix) return undefined;
	const coder: CloudCpProjectCoderConfig = {};
	if (templateId) {
		coder.templateId = templateId;
		// Only send a rich parameter the template actually declares.
		if (state.supportedParams.includes("size")) coder.size = state.size;
		if (state.supportedParams.includes("startup_script") && state.startupScript.trim().length > 0) {
			coder.startupScript = state.startupScript;
		}
	}
	if (extraRepos.length > 0) coder.extraRepos = extraRepos;
	if (workspaceNamePrefix) coder.workspaceNamePrefix = workspaceNamePrefix;
	return coder;
}
