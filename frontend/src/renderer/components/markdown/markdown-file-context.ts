import type { FilesSource } from "../../hooks/useSessionWorkspaceFiles";
import { createContext } from "react";

export type MarkdownFileContextValue = {
	sessionId: string;
	hostId?: string;
	filePath: string;
	artifactOrigin?: string;
 snapshotSource?: Extract<FilesSource,{kind:"snapshot"}>;
	version: number;
};

export const MarkdownFileContext = createContext<MarkdownFileContextValue | null>(null);
