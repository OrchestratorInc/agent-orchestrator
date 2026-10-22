import { normalizeCodeRowText } from "./file-annotations";
import type { FileCodeReference, FileReferenceLine } from "./file-annotations";

/** Durable user-supplied selection; source hints never authorize filesystem access. */
export interface ChatFileReference extends Omit<FileCodeReference, "source"> {
	id: string;
	order: number;
	sessionId: string;
	hostId?: string;
	source: { kind: "workspace" | "pull_request" | "artifact"; number?: number; snapshot?: string; url?: string };
	truncated: boolean;
}

export function captureFileReference(selection: FileCodeReference, sessionId: string, hostId?: string): ChatFileReference {
	let budget = 8000;
	let truncated = false;
	const lines: FileReferenceLine[] = [];
	for (const row of selection.lines) {
		if (budget <= 1) {
			truncated = true;
			break;
		}
		const captured = normalizeCodeRowText(row.text);
		const text = [...captured].slice(0, Math.min(700, budget - 1)).join("");
		truncated ||= text !== captured;
		lines.push({ ...row, text });
		budget -= [...text].length + 1;
	}
	return {
		...selection,
		id: crypto.randomUUID(),
		order: Date.now(),
		sessionId,
		hostId,
		source: {
			kind: selection.source?.kind ?? "workspace",
			number: selection.source?.number,
			snapshot: selection.source?.snapshot,
			url: selection.source?.kind === "pull_request" ? selection.source.url : undefined,
		},
		lines,
		truncated,
	};
}

export function fileReferenceLabel(ref: ChatFileReference): string {
	const name = ref.path.split("/").at(-1) ?? ref.path;
	const side = ref.endSide && ref.endSide !== ref.side ? `${ref.side} → ${ref.endSide}` : ref.side;
	return `${name} · L${ref.line}${ref.endLine === ref.line ? "" : `–${ref.endLine}`} · ${side}`;
}
export function fileReferencePreview(ref: ChatFileReference): string {
	return ref.lines.map((row) => `${ref.side === "file" ? "" : row.kind === "add" ? "+" : row.kind === "del" ? "-" : " "}${row.text}`).join("\n");
}

export interface FileAnnotationRange extends Pick<ChatFileReference, "line" | "endLine" | "side" | "lines"> {
	requestKey: number;
}
export interface FileAnnotationResolution {
	state: "unchanged" | "changed" | "unavailable" | "moved";
	reason?: string;
	path?: string;
	line?: number;
	endLine?: number;
	side?: string;
	oldLineOffset?: number;
	newLineOffset?: number;
}
export type OpenFileAnnotation = (reference: ChatFileReference, resolution: FileAnnotationResolution) => void;
