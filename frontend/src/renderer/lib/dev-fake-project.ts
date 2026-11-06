import type { WorkspaceSummary } from "../types/workspace";
import { IS_DEV } from "./is-dev";

// Dev builds only: a pretend project for previewing the guided tour on a
// machine that already has projects. It exists only in the renderer's project
// list (fetchWorkspaces appends it); nothing is created on disk or in the
// daemon, and the tour drops it when it ends.

export const DEV_FAKE_PROJECT_ID = "dev-fake-project";

let enabled = false;
const sendListeners = new Set<() => void>();

export function setDevFakeProject(on: boolean) {
	enabled = IS_DEV && on;
}

export function isDevFakeProject(projectId: string | undefined): boolean {
	return IS_DEV && enabled && projectId === DEV_FAKE_PROJECT_ID;
}

/** A send in the pretend project's new chat: it goes nowhere, but the tour
 * treats it as the first task sent. */
export function noteDevFakeSend() {
	for (const listener of sendListeners) listener();
}

export function onDevFakeSend(listener: () => void) {
	sendListeners.add(listener);
	return () => {
		sendListeners.delete(listener);
	};
}

export function withDevFakeProject(workspaces: WorkspaceSummary[]): WorkspaceSummary[] {
	if (!IS_DEV || !enabled || workspaces.some((workspace) => workspace.id === DEV_FAKE_PROJECT_ID)) return workspaces;
	return [{ id: DEV_FAKE_PROJECT_ID, name: "demo-app", path: "/tmp/demo-app", orchestratorAgent: "claude-code", sessions: [] }, ...workspaces];
}
