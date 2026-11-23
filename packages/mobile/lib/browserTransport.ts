// Types from the M1 transport seam's Interfaces block (mobile browser plan),
// copied verbatim so typing builds against them before M1 lands. M1's file
// replaces this one.
import type { BrowserLiveCommand, BrowserLiveState } from "./browserLive";
import type { BrowserLiveFrame } from "./browserLiveProtocol";

export type BrowserControl = "agent" | "human" | "none";
// M5 adds `field`: an opaque name for the focused field, the same only while
// the same field keeps focus (two password fields are otherwise identical).
export type FocusedEditable = { kind: "text"|"password"|"email"|"number"|"url"|"search"|"tel"|"textarea"|"contenteditable"; value?: string; field?: string } | null;
export type BrowserViewState = BrowserLiveState & { control: BrowserControl; focusedEditable: FocusedEditable };
export type BrowserFrame = BrowserLiveFrame;
export type BrowserCommand =
	| BrowserLiveCommand
	| { type: "viewport"; payload: { width: number; height: number; dpr: number; mobile: boolean } | null }
	| { type: "quality"; payload: { quality: number; maxFps: number } }
	| { type: "control"; payload: { action: "take" | "release" } }
	| { type: "dialog"; payload: { accept: boolean; promptText?: string } };
