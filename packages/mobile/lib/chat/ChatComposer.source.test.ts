import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

const composer = source("./ChatComposer.tsx");
// The dock and pill rules ChatComposer shares with ReviewerComposer.
const composerStyles = source("./composerStyles.ts");

/** The body of one entry in the composer's stylesheet, shared or its own. */
function styleRule(name: string): string {
	const rule = new RegExp(`\\n\\t${name}: \\{([^}]*)\\}`);
	const match = composerStyles.match(rule) ?? composer.match(rule);
	expect(match, `${name} rule`).not.toBeNull();
	return match?.[1] ?? "";
}

describe("chat composer pill", () => {
	// Picked photos and files ride inside the pill over the field, as part of
	// the message, rather than as a strip of chips stacked on top of it.
	it("draws attachments inside the pill, over the field", () => {
		const pill = composer.slice(composer.indexOf("{requestCard ?? <View style={[styles.composer"));
		const tray = pill.indexOf("<ComposerAttachmentTray items={trayItems}");
		expect(tray).toBeGreaterThan(0);
		expect(tray).toBeLessThan(pill.indexOf("<TextInput"));
		expect(composer.indexOf("<ComposerAttachmentTray")).toBe(composer.indexOf("<ComposerAttachmentTray items={trayItems}"));
		expect(composer).toContain("useMemo(() => composerTrayItems(attachments), [attachments])");
	});

	// The row is one capsule: attach, field, dictation and the one filled control
	// that commits. A border plus a fill made it read as a box with buttons in it.
	it("draws a single borderless capsule", () => {
		const pill = styleRule("composer");
		expect(pill).toContain("borderRadius: COMPOSER_RADIUS");
		expect(composer).toContain("...composerStyleRules(t),");
		expect(pill).not.toContain("borderWidth");
		expect(pill).not.toContain("borderColor");
		// No fill under the glass: an opaque pill under a material is the one
		// arrangement that turns it grey. Android, which has no material, keeps the
		// elevated fill.
		expect(pill).toContain('backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated');
	});

	it("lays the glass behind the row, sized by the pill itself", () => {
		// The material is handed a radius and nothing else: the pill owns its height,
		// the host fills the pill and the material fills the host. Three attempts at
		// sizing it from something read at runtime — a layout measurement, the space
		// SwiftUI was proposed mid-animation, a content-height count — each ended with
		// the material a different size from the pill behind it, and all three are read
		// while the keyboard is still moving. Filling reads nothing.
		expect(composer).toContain("<ComposerGlass radius={COMPOSER_RADIUS} />");
		expect(composer).not.toContain("<ComposerGlass height=");
		// The field grows by itself while glass fills the pill: no measured height
		// on either. One set from the content-size event lagged a line behind, so
		// each new line first scrolled the field and cut off its top line.
		expect(composer).not.toContain("onContentSizeChange");
		expect(composer).not.toContain("fieldHeight");
		expect(styleRule("composer")).toContain("minHeight: COMPOSER_HEIGHT");
		expect(styleRule("composerRow")).toContain("maxHeight: COMPOSER_MAX_HEIGHT");
		expect(styleRule("composerRow")).toContain('alignItems: "flex-end"');
		expect(styleRule("input")).toContain("minHeight: COMPOSER_FIELD_HEIGHT");
		expect(styleRule("input")).toContain("maxHeight: COMPOSER_FIELD_MAX_HEIGHT");
		// `false`: the material must not take the touch. The field is inside this
		// pill, and an interactive material only passes taps that land on its own
		// content — typing would stop working.
		expect(source("./composer-glass.ios.tsx")).toContain("glassPanel(radius, undefined, false)");
		expect(source("./composer-glass.ios.tsx")).toContain("frame({ maxWidth: 2000, maxHeight: 2000 })");
	});

	it("does not clear a newer draft when an earlier send completes", () => {
		expect(composer).toContain('const unchanged = current === text && currentQuotes === quotes;');
		expect(composer).toContain('unchanged ? AsyncStorage.removeItem(draftKey)');
		expect(composer).toContain('if (latestText.current === text && latestQuotes.current === quotes) {');
	});

	it("uses the latest native text when a pasted draft grows before React renders", () => {
		expect(composer).toContain("onChangeText={(value) => { latestText.current = value; setText(value); }}");
		expect(composer).toContain('latestText.current = "";\n\t\t\tsetText("");');
	});

	// A running turn shows only its stop button: no mic beside it.
	it("drops the mic while the stop button shows, and keeps the send a filled circle", () => {
		expect(composer).not.toContain("MicKey");
		expect(styleRule("send")).toContain("backgroundColor: t.accent");
	});

	// The press used to tap and the landing to buzz success: two haptics for one
	// send. The one that stays is the outcome.
	it("fires one haptic per send, when it lands", () => {
		expect(composer).toContain('onPress={() => void submit("send")}');
		expect(composer).toContain('onPress={() => void submit("steer")}');
		expect(composer).not.toMatch(/haptics\.tap\(\);\s*void submit/);
		expect(composer).not.toMatch(/haptics\.tap\(\);\s*set(Promoting|Cancelling)QueuedTurnId/);
	});

	it("gives every small control in and around the pill a full touch target", () => {
		for (const rule of ["removeQuote", "queueSteer", "queueDelete", "steerAction", "roundTarget"]) {
			expect(styleRule(rule), rule).toContain("width: touchTarget, height: touchTarget");
		}
		expect(composer).toContain("hitSlop={{ top: CHIP_SLOP, bottom: CHIP_SLOP }}");
		expect(composer).not.toContain("hitSlop={7}");
	});

	// The button alone would announce a bare "Answer", with no word of which
	// request it brings back.
	it("names the request a screen reader would bring back", () => {
		expect(composer).toContain("accessibilityLabel={`${request.title}. Answer it`}");
		expect(composer).toContain('accessibilityActions={[{ name: "activate", label: "Answer" }]}');
		expect(composer).toContain("onAccessibilityTap={onRestoreRequest}");
		expect(composer).toContain('<View importantForAccessibility="no-hide-descendants">\n\t\t\t\t\t<Button title="Answer"');
	});

	// The level is otherwise only a bar's width and colour.
	it("gives the context meter a value a screen reader can read", () => {
		expect(composer).toContain('<View accessible accessibilityRole="progressbar" accessibilityLabel="Context window" accessibilityValue={contextMeter.accessibilityValue}');
	});

	it("opens the row with a plus rather than a paperclip", () => {
		expect(source("./ChatAttachmentMenu.tsx")).toContain('<AppSymbol name="add"');
	});

	// The dock used to swap its inset for the keyboard gap on a visibility flag,
	// and that flag turns over when the keyboard has finished hiding — so the
	// composer, and the model label above it, spent the whole closing animation one
	// inset too low and then jumped into place at the end.
	it("rides the keyboard's own progress instead of switching inset on a flag", () => {
		expect(composer).toContain("useReanimatedKeyboardAnimation()");
		expect(composer).toContain("(restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value)");
		expect(composer).toContain("{ paddingBottom: restingInset }");
		expect(composer).not.toContain("dockInset(");
	});
});

describe("composer controls row", () => {
	const control = source("./ChatTurnSettingsControl.tsx");

	// "GPT-5.6-Sol · Medium · Ask when unsure" is the longest thing in the row, and
	// the delivery note or steer can join it. The settings control is the one
	// that yields, clipped so a label that fails to truncate cannot paint over its
	// neighbours.
	it("lets the settings label yield", () => {
		expect(styleRule("settingsSlot")).toContain("flex: 1, minWidth: 0");
		expect(styleRule("settingsSlot")).toContain("overflow: \"hidden\"");
		expect(control).toContain('<Text maxLines={1} overflow="ellipsis"');
	});
});

describe("collapsed and expanded composer", () => {
	// Collapsed, a slim pill under the session's chips; writing grows it into a
	// card with the turn settings between attach and the round button. The mic
	// takes it back to the slim pill.
	it("opens on focus and stays open while anything waits to be sent, until the mic opens", () => {
		expect(composer).toContain("const dictating = listening || transcribing;");
		// `keyboardSend.holding` covers a hardware-Return send, whose read-only field
		// is away for a moment after the draft clears (see useKeyboardSendRefocus).
		expect(composer).toContain("const expanded = !dictating && (focused || keyboardSend.holding || hasDraft || attachments.length > 0);");
		expect(composer).toContain('<Animated.View pointerEvents={expanded ? "box-none" : "none"}');
	});

	// Moving the field between two parents would remount it and drop the keyboard.
	it("keeps the field in one row in both shapes", () => {
		expect(composer.match(/<TextInput\n/g)).toHaveLength(1);
		expect(composer.match(/\{attachMenu\}/g)).toHaveLength(1);
		expect(composer.match(/\{roundButton\}/g)).toHaveLength(1);
	});

	// Reduce Motion turns the opening spring into a cut.
	it("opens without motion under Reduce Motion", () => {
		expect(composer).toContain("openness.value = reduceMotion ? withTiming(expanded ? 1 : 0, { duration: 0 }) : withSpring(expanded ? 1 : 0, COMPOSER_SPRING);");
	});

	// The round button: stop the mic, stop the turn, send, or dictate, by what
	// the prompt holds. Dictating drops the keyboard.
	it("follows the prompt with one round button", () => {
		expect(composer).toContain('const primary = listening ? "stopDictation" : transcribing ? "transcribing" : primaryAction === "stop" ? "stopTurn" : nothingToSend && canDictate ? "dictate" : "send";');
		expect(composer).toContain('onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.toggleLatched(); }}');
	});
});

describe("composer chips", () => {
	// The session's PR, what it changed and the context meter sit over the
	// composer, open or not.
	it("shows the session's state as chips that say where they lead", () => {
		expect(composer).toContain("const chips = activeReviewPR || changes || contextMeter ?");
		expect(composer).toContain('accessibilityHint="Opens the pull request review" onPress={onOpenReview}');
		expect(composer).toContain('accessibilityHint="Opens the changed files" onPress={changes.onOpen}');
	});

	// Steering stays reachable from the open card. While a turn runs the field's
	// placeholder says a message waits for it; the controls row repeats nothing.
	it("keeps the steer action in the card, with no delivery note beside it", () => {
		expect(composer).toContain('accessibilityLabel="Steer this turn now"');
		expect(composer).not.toContain("Sent after this");
		expect(composer).not.toContain("deliveryNote");
	});
});
