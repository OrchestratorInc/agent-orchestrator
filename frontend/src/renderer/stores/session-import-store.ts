import { create } from "zustand";

type SessionImportDialog = { kind: "project"; projectId: string } | { kind: "folders" };

type SessionImportState = {
	dialog: SessionImportDialog | null;
	dismissedProjects: string[];
	importedCounts: Record<string, number>;
	openProjectImport: (projectId: string) => void;
	openFolderImport: () => void;
	closeDialog: () => void;
	dismissProject: (projectId: string) => void;
	recordImport: (projectId: string, count: number) => void;
};

const dismissedProjectsStorageKey = "ao.sessionImport.dismissedProjects";

function initialDismissedProjects(): string[] {
	try {
		const stored: unknown = JSON.parse(window.localStorage.getItem(dismissedProjectsStorageKey) ?? "[]");
		return Array.isArray(stored) ? stored.filter((id): id is string => typeof id === "string") : [];
	} catch {
		return [];
	}
}

export const useSessionImportStore = create<SessionImportState>((set, get) => ({
	dialog: null,
	dismissedProjects: initialDismissedProjects(),
	importedCounts: {},
	openProjectImport: (projectId) => set({ dialog: { kind: "project", projectId } }),
	openFolderImport: () => set({ dialog: { kind: "folders" } }),
	closeDialog: () => set({ dialog: null }),
	dismissProject: (projectId) => {
		const dismissedProjects = [...new Set([...get().dismissedProjects, projectId])];
		try {
			window.localStorage.setItem(dismissedProjectsStorageKey, JSON.stringify(dismissedProjects));
		} catch {}
		set({ dismissedProjects });
	},
	recordImport: (projectId, count) =>
		set((state) => ({ importedCounts: { ...state.importedCounts, [projectId]: (state.importedCounts[projectId] ?? 0) + count } })),
}));
