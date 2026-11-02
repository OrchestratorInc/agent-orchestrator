import type { ElementContent, Root } from "hast";
import type { GrammarName } from "./code-highlight";

export type CodeLine = { tokens: ElementContent[]; signature: string };
export type StreamingCode = { source: string; language: GrammarName; lines: CodeLine[] };

export type HighlightWorkerRequest =
	| { kind: "highlight"; id: number; code: string; language: GrammarName }
	| { kind: "stream"; id: number; streamId: number; offset: number; reset: boolean; code: string; language: GrammarName }
	| { kind: "release"; streamId: number };

export type HighlightWorkerResponse =
	| { kind: "highlight"; id: number; tree?: Root }
	| { kind: "stream"; id: number; lines: CodeLine[] }
	| { kind: "reset"; id: number }
	| { kind: "unavailable"; id: number };
