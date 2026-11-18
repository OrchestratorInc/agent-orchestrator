import type { ElementContent, Root } from "hast";
import type { GrammarName } from "./code-highlight";

export type CodeLine = { tokens: ElementContent[]; signature: string };

export type HighlightWorkerRequest = { kind: "highlight"; id: number; code: string; language: GrammarName };

export type HighlightWorkerResponse =
	| { kind: "highlight"; id: number; tree?: Root }
	| { kind: "unavailable"; id: number };
