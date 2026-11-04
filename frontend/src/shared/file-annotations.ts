// Shared with the mobile app, which sends the same feedback format to the agent.
export * from "../../../packages/shared/files/file-annotations";
import type { FileCodeReference as SharedFileCodeReference, FileReferenceLine as SharedFileReferenceLine } from "../../../packages/shared/files/file-annotations";

export type FileReferenceLine = SharedFileReferenceLine & { side?: "file" | "old" | "new" };
export type FileCodeReference = Omit<SharedFileCodeReference, "lines"> & {
 endSide?: "file" | "old" | "new";
 previousPath?: string;
 source?: { kind: "workspace" | "pull_request" | "artifact"; number?: number; snapshot?: string; url?: string };
 scope?: string;
 commitSha?: string;
 fileFingerprint?: string;
 workspaceVersion?: string;
 lines: FileReferenceLine[];
};

/** Strip one viewer row terminator, preserving real whitespace and invalid embedded breaks. */
export function normalizeCodeRowText(text: string): string {
 const row = text.replace(/(?:\r\n|\r|\n)$/, "");
 return /[\r\n]/.test(row) ? text : row;
}
