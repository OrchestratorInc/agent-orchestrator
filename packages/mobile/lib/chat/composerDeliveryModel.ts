export type ComposerDeliveryIntent = "send" | "steer";

export function composerDeliveryRoute(intent: ComposerDeliveryIntent, steerEligible: boolean): "queue" | "steer" {
	return intent === "steer" && steerEligible ? "steer" : "queue";
}

export function composerDeliveryPresentation(state: { active: boolean; canSteer: boolean; hasDraft: boolean; hasAttachments: boolean; hasQueued: boolean }) {
	return {
		placeholder: state.active ? "Send after this…" : "Message this worker…",
		showSteerAction: state.active && state.canSteer && state.hasDraft && !state.hasAttachments && !state.hasQueued,
	};
}

export function composerPrimaryAction(state: { active: boolean; hasDraft: boolean; hasAttachments: boolean }): "send" | "stop" {
	return state.active && !state.hasDraft && !state.hasAttachments ? "stop" : "send";
}

/** Which modifiers were held with Return on a hardware keyboard. */
export type HardwareReturnModifiers = { command: boolean; control: boolean; option: boolean };

/**
 * Return on a hardware keyboard, matching the desktop composer: Return and
 * Option+Return send, Cmd+Return or Ctrl+Return steer the running turn. A steer
 * the draft is not eligible for still falls back to a send (see
 * `composerDeliveryRoute`). Shift+Return never reaches here; it inserts a newline.
 */
export function hardwareReturnIntent(modifiers: HardwareReturnModifiers): ComposerDeliveryIntent {
	return modifiers.command || modifiers.control ? "steer" : "send";
}
