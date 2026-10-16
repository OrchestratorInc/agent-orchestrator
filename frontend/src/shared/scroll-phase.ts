/** Main → renderer: trackpad fingers touched ("down"), left ("up"), or the gesture was cancelled. */
export const SCROLL_PHASE_CHANNEL = "app:scrollPhase";

export type ScrollPhase = "down" | "up" | "cancel";
