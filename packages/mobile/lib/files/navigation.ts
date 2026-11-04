import { createContext, useContext } from "react";

/** How a session surface opens its Files screens. Absent where a surface has no session to open. */
export type FilesNavigation = {
	openChanges(): void;
	openFile(path: string, previousPath?: string): void;
};

export const FilesNavigationContext = createContext<FilesNavigation | null>(null);

export function useFilesNavigation(): FilesNavigation | null {
	return useContext(FilesNavigationContext);
}

export function filesRoute(sessionId: string, hostId?: string, title?: string) {
	return { pathname: "/files/[sessionId]" as const, params: { sessionId, hostId, title } };
}

export function fileRoute(sessionId: string, path: string, hostId?: string, previousPath?: string) {
	return { pathname: "/files/[sessionId]/file" as const, params: { sessionId, path, mode: "diff", previousPath, hostId } };
}

/**
 * Distinct paths the conversation's turns report changing — whether the header
 * shows its "has changes" dot. It reads the turns already on screen, so it costs
 * no request; it is a hint, not a count to show, since only git (the Files
 * screen) knows what the worktree holds now.
 */
export function changedPathCount(turns: ReadonlyArray<{ diff?: { files: ReadonlyArray<{ path: string }> } }> | undefined): number {
	if (!turns?.length) return 0;
	const paths = new Set<string>();
	for (const turn of turns) for (const file of turn.diff?.files ?? []) paths.add(file.path);
	return paths.size;
}
