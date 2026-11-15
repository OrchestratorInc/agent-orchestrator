import { create } from "zustand";
import type { CloudCpProjectCoderConfig, CloudCpSessionRepo } from "../lib/cloud-cp/types";

export type CoderSize = "small" | "medium" | "large";

export interface CoderSessionOptionsState {
	templateId: string;
	supportedParams: string[];
	size: CoderSize;
	startupScript: string;
	extraRepos: CloudCpSessionRepo[];
	workspaceNamePrefix: string;
	parameters: Record<string, string>;
	setTemplate: (templateId: string, supportedParams: string[]) => void;
	setParameters: (parameters: Record<string, string>) => void;
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
	parameters: {} as Record<string, string>,
};

export const useCoderSessionOptionsStore = create<CoderSessionOptionsState>((set) => ({
	...initialState,
	setTemplate: (templateId, supportedParams) =>
		set((state) => (state.templateId === templateId ? { supportedParams } : { templateId, supportedParams, parameters: {} })),
	setParameters: (parameters) => set({ parameters }),
	setSize: (size) => set({ size }),
	setStartupScript: (startupScript) => set({ startupScript }),
	setExtraRepos: (extraRepos) => set({ extraRepos }),
	setWorkspaceNamePrefix: (workspaceNamePrefix) => set({ workspaceNamePrefix }),
	reset: () => set({ ...initialState, supportedParams: [], extraRepos: [], parameters: {} }),
}));

export function buildCoderRequestOptions(state: {
	templateId: string;
	supportedParams: string[];
	size: CoderSize;
	startupScript: string;
	extraRepos: CloudCpSessionRepo[];
	workspaceNamePrefix?: string;
	parameters?: Record<string, string>;
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
		if (state.supportedParams.includes("size")) coder.size = state.size;
		if (state.supportedParams.includes("startup_script") && state.startupScript.trim().length > 0) {
			coder.startupScript = state.startupScript;
		}
		const parameters = state.parameters ?? {};
		if (Object.keys(parameters).length > 0) coder.parameters = { ...parameters };
	}
	if (extraRepos.length > 0) coder.extraRepos = extraRepos;
	if (workspaceNamePrefix) coder.workspaceNamePrefix = workspaceNamePrefix;
	return coder;
}
