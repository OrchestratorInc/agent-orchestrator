import type { EffortChoice } from "../modelEffort";
import { excerptDraftText } from "./responseQuotes";
import type { ChatExcerpt } from "./types";

/*
 * Claude Code's Ultracode: the effort slider's top stop, above the model's
 * own levels. Claude Code turns it on for a turn when the message names it,
 * so a message asking for it starts "Ultracode:" (desktop's withUltracode).
 * It runs at Extra high, so picking it saves Extra high where the model has
 * it; on a model without, the level stays as it is and only the message
 * asks for it.
 */

/** The slider's stop for Ultracode. */
export const ULTRACODE = "ultracode";
/** The level Claude Code's Ultracode runs at, saved with it where the model has it. */
export const ULTRACODE_LEVEL = "xhigh";
/**
 * The stop before Ultracode on a model with no levels (Claude Haiku): the
 * model's own effort, which saves nothing. Named as spawn's "__auto__" is, so
 * no level can share it.
 */
export const DEFAULT_EFFORT_STOP = "__default__";
/** The one agent with Ultracode. */
export const ULTRACODE_HARNESS = "claude-code";

/**
 * Whether a chat offers Ultracode: Claude Code on a machine of the user's own.
 * Orchestrator Cloud's chats have none, as on desktop.
 */
export function offersUltracode(harness: string | undefined, local = true): boolean {
	return local && harness === ULTRACODE_HARNESS;
}

/**
 * The slider's stops where Ultracode is offered: the model's levels, then
 * Ultracode, checked in place of any level while it is on. A model with no
 * levels gets a "Default" stop before it, so there is still somewhere to
 * turn it off.
 */
export function withUltracodeStop(levels: readonly EffortChoice[], on: boolean): EffortChoice[] {
	const below = levels.length ? levels : [{ value: DEFAULT_EFFORT_STOP, label: "Default", selected: true }];
	return [
		...below.map((choice) => (on && choice.selected ? { ...choice, selected: false } : choice)),
		{ value: ULTRACODE, label: "Ultracode", selected: on },
	];
}

/**
 * The level saved with Ultracode: Extra high where the model offers it, else
 * none, and the level in effect stays.
 */
export function ultracodeLevel(levels: readonly string[]): string | undefined {
	return levels.includes(ULTRACODE_LEVEL) ? ULTRACODE_LEVEL : undefined;
}

/**
 * Where a conversation's Ultracode is kept on the phone: the daemon has no
 * field for it. `machine` is the store's machine identity, as the chat keys
 * its draft, so a New task can turn it on for the chat it opens.
 */
export function ultracodeKey(machine: string, sessionId: string): string {
	return `ao.chat.ultracode.${machine}.${sessionId}`;
}

/** The message as sent with Ultracode on: named once, at its start. */
export function withUltracode(text: string): string {
	return /\bultracode\b/i.test(text) ? text : `Ultracode:\n${text}`;
}

/**
 * Whether a retried send, recorded as the draft it was typed as
 * (`retriedDraft`, without Ultracode's prefix), was this reply. Exact: a draft
 * changed since, even to or from the prefix's words, is kept.
 */
export function isSentDraft(typedDraft: string, reply: string, excerpts?: readonly ChatExcerpt[]): boolean {
	return excerptDraftText(reply, excerpts) === typedDraft;
}
