import { createContext, useContext } from "react";
import type { SourceRef } from "../environment/scopedBoard";

/** How a session surface opens its Files screens. Absent where a surface has no session to open. */
export type FilesNavigation = {
	openChanges(): void;
	openFile(path: string, previousPath?: string): void;
};

export const FilesNavigationContext = createContext<FilesNavigation | null>(null);

export function useFilesNavigation(): FilesNavigation | null {
	return useContext(FilesNavigationContext);
}

export type FilesRouteOwner = { source: SourceRef; scopeKey?: string; repository?: string };

function ownerParams(hostId: string | undefined, owner: FilesRouteOwner | undefined) {
	return owner ? {
		...(owner.source.kind === "local" ? { hostId: owner.source.id } : {}),
		source: owner.source.kind, sourceId: owner.source.id, scopeKey: owner.scopeKey,
		...(owner.source.kind === "cloud" && owner.repository ? { repository: owner.repository } : {}),
	} : { hostId };
}

export function filesRoute(sessionId: string, hostId?: string, title?: string, owner?: FilesRouteOwner) {
	return { pathname: "/files/[sessionId]" as const, params: { sessionId, ...ownerParams(hostId, owner), title } };
}

export function fileRoute(sessionId: string, path: string, hostId?: string, previousPath?: string, owner?: FilesRouteOwner) {
	return { pathname: "/files/[sessionId]/file" as const, params: { sessionId, path, mode: "diff", previousPath, ...ownerParams(hostId, owner) } };
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
