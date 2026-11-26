import { excerptDraftText } from "./responseQuotes";
import type { ChatExcerpt } from "./types";

/**
 * Claude Code's Ultracode: the effort above Extra High. Claude Code turns it
 * on for a turn when the message names it, so a message asking for it starts
 * "Ultracode:" (desktop's withUltracode). The level saved is Extra High.
 */
export const ULTRACODE = "ultracode";
export const ULTRACODE_LEVEL = "xhigh";

/** Whether a chat's agent and model offer Ultracode: Claude Code, on a model that takes Extra High. */
export function offersUltracode(harness: string | undefined, levels: readonly string[]): boolean {
	return harness === "claude-code" && levels.includes(ULTRACODE_LEVEL);
}

/** The message as sent with Ultracode on: named once, at its start. */
export function withUltracode(text: string): string {
	return /\bultracode\b/i.test(text) ? text : `Ultracode:\n${text}`;
}

/**
 * Whether a send recorded as `sentDraft` (a retried send's record) was this
 * reply, sent as typed or with Ultracode's prefix: the record holds what was
 * sent, the composer what was typed, and a mismatch kept a delivered draft to
 * be sent again.
 */
export function isSentDraft(sentDraft: string, reply: string, excerpts?: readonly ChatExcerpt[]): boolean {
	return excerptDraftText(reply, excerpts) === sentDraft || excerptDraftText(withUltracode(reply), excerpts) === sentDraft;
}
