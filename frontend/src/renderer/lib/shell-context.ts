import { createContext, useContext } from "react";
import type { components } from "../../api/schema";
import type { useDaemonStatus } from "../hooks/useDaemonStatus";
import type { RemoteHost } from "../hooks/useRemoteHosts";

export type ShellContextValue = {
	daemonStatus: ReturnType<typeof useDaemonStatus>;
	workspaceStartupState: "loading" | "ready" | "error";
	createProject: (input: {
		path: string;
		workerAgent: string;
		orchestratorAgent: string;
		reviewerAgent?: string;
		trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
		asWorkspace?: boolean;
	}) => Promise<void>;
	cloneProject: (input: {
		remoteUrl: string;
		destinationParent: string;
		workerAgent: string;
		orchestratorAgent: string;
		reviewerAgent?: string;
		trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
	}) => Promise<void>;
	initializeProjectRepository: (path: string) => Promise<void>;
	openRemoteProjectSettings: (hostId: string, projectId: string) => void;
	remoteHosts?: RemoteHost[];
	retryRemoteHosts?: () => void;
	removeProject?: (projectId: string, cloud: boolean) => void;
	openProject?: (projectId: string) => void;
	validateImport?: (input: {
		path: string;
		importKind: "project" | "workspace";
	}) => Promise<components["schemas"]["ImportValidationResult"]>;
};

const ShellContext = createContext<ShellContextValue | null>(null);

export const ShellProvider = ShellContext.Provider;

export function useShell(): ShellContextValue {
	const ctx = useContext(ShellContext);
	if (!ctx) throw new Error("useShell must be used within the _shell layout route");
	return ctx;
}

export function useShellMaybe(): ShellContextValue | null {
	return useContext(ShellContext);
}
