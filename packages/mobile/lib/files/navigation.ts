import { createContext, useContext } from "react";

/** How a session surface opens its Files screens. Absent where a surface has no session to open. */
export type FilesNavigation = {
	openChanges(): void;
	/** `line` is a line a chat link cited; the file then opens at it. */
	openFile(path: string, previousPath?: string, line?: number): void;
	/**
	 * The worktree file a bare name in chat (`README.md`) names: its path, null
	 * when the session has none (or several) by that name, undefined until the
	 * file list has loaded. Absent where the surface has no file list.
	 */
	bareFile?(name: string): string | null | undefined;
	/** Loads the file list `bareFile` reads, the first time a bare name needs it. */
	wantFiles?(): void;
};

export const FilesNavigationContext = createContext<FilesNavigation | null>(null);

export function useFilesNavigation(): FilesNavigation | null {
	return useContext(FilesNavigationContext);
}

export function filesRoute(sessionId: string, hostId?: string, title?: string) {
	return { pathname: "/files/[sessionId]" as const, params: { sessionId, hostId, title } };
}

/**
 * A changed file opens on its diff. A cited line opens the whole file instead:
 * a diff folds away the unchanged lines a link most often points at.
 */
export function fileRoute(sessionId: string, path: string, hostId?: string, previousPath?: string, line?: number) {
	return line
		? { pathname: "/files/[sessionId]/file" as const, params: { sessionId, path, mode: "file", line: String(line), previousPath, hostId } }
		: { pathname: "/files/[sessionId]/file" as const, params: { sessionId, path, mode: "diff", previousPath, hostId } };
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
