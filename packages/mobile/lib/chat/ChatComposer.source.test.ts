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
	// The row is one capsule: attach, field, dictation and the one filled control
	// that commits. A border plus a fill made it read as a box with buttons in it.
	it("draws a single borderless capsule", () => {
		const pill = styleRule("composer");
		expect(pill).toContain("borderRadius: COMPOSER_RADIUS");
		// Half the resting height: 28 on iOS, around a row of 44pt controls, and
		// 30 on Android, where the controls are Material's 48dp.
		expect(composerStyles).toContain("const COMPOSER_HEIGHT = touchTarget + space.xs * 2");
		expect(composerStyles).toContain("const COMPOSER_RADIUS = COMPOSER_HEIGHT / 2");
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
		// Open or closed, one pill with one corner: it grows rather than becoming a card.
		expect(composer).toContain("<ComposerGlass radius={COMPOSER_RADIUS} />");
		expect(composer).not.toContain("<ComposerGlass height=");
		// The field grows from the native content size while glass fills the pill,
		// avoiding a separately measured glass height that could lag behind it.
		expect(composer).toContain("onContentSizeChange={(event) => {");
		expect(composer).toContain("style={[styles.input, { height: text ? fieldHeight : COMPOSER_FIELD_HEIGHT }]}");
		expect(styleRule("composer")).toContain("minHeight: COMPOSER_HEIGHT");
		expect(styleRule("composerRow")).toContain("maxHeight: COMPOSER_MAX_HEIGHT");
		expect(styleRule("composerRow")).toContain('alignItems: "flex-end"');
		expect(styleRule("input")).toContain("minHeight: COMPOSER_FIELD_HEIGHT");
		expect(styleRule("input")).toContain("maxHeight: COMPOSER_FIELD_MAX_HEIGHT");
		expect(composer).toContain("const [fieldHeight, setFieldHeight] = useState(COMPOSER_FIELD_HEIGHT)");
		expect(composer).toContain("const contentHeight = Math.ceil(event.nativeEvent.contentSize.height)");
		expect(composer).toContain("setFieldHeight(Math.max(COMPOSER_FIELD_HEIGHT, Math.min(COMPOSER_FIELD_MAX_HEIGHT, contentHeight)))");
		// `false`: the material must not take the touch. The field is inside this
		// pill, and an interactive material only passes taps that land on its own
		// content — typing would stop working.
		expect(source("./composer-glass.ios.tsx")).toContain("glassPanel(radius, undefined, false)");
		expect(source("./composer-glass.ios.tsx")).toContain("frame({ maxWidth: 2000, maxHeight: 2000 })");
	});

	it("returns an emptied multiline draft to the one-line height", () => {
		// A controlled TextInput can retain its last native content size after its
		// value is cleared; the empty placeholder must not inherit that height.
		expect(composer).toMatch(/setText\(""\);\s*setFieldHeight\(COMPOSER_FIELD_HEIGHT\);/);
		expect(composer).toMatch(/if \(!latestText.current\) \{\s*setFieldHeight\(COMPOSER_FIELD_HEIGHT\);/);
		expect(composer).toContain("height: text ? fieldHeight : COMPOSER_FIELD_HEIGHT");
	});

	it("does not clear a newer draft when an earlier send completes", () => {
		expect(composer).toContain('const unchanged = current === text && currentQuotes === quotes;');
		expect(composer).toContain('unchanged ? AsyncStorage.removeItem(draftKey)');
		expect(composer).toContain('if (latestText.current === text && latestQuotes.current === quotes) {');
	});

	it("uses the latest native text when a pasted draft grows before React renders", () => {
		expect(composer).toContain("onChangeText={(value) => { latestText.current = value; setText(value); }}");
		expect(composer).toContain("if (!latestText.current) {");
		expect(composer).toContain('latestText.current = "";\n\t\t\tsetText("");');
	});

	// A running turn shows only its stop button: no mic beside it.
	it("drops the mic while the stop button shows, and keeps the send a filled circle", () => {
		expect(composer).not.toContain("MicKey");
		expect(styleRule("send")).toContain("width: touchTarget, height: touchTarget, borderRadius: radius.pill");
		expect(styleRule("send")).toContain("backgroundColor: t.accent");
	});

	// The glyph on the filled accent is the accent's own ink. `bgBase` happened
	// to match it on the default themes and nowhere else.
	it("inks the send glyph for the accent fill", () => {
		expect(composer).toContain('<AppSymbol name="send" size={iconSize.sm} weight="bold" color={t.onAccent} />');
		// The disc is the plus's circle size, inside a full touch target.
		expect(styleRule("roundTarget")).toContain("width: touchTarget, height: touchTarget");
		expect(styleRule("roundDisc")).toContain("width: COMPOSER_DISC, height: COMPOSER_DISC");
		expect(source("./ChatAttachmentMenu.tsx")).toContain("width: COMPOSER_DISC, height: COMPOSER_DISC");
		expect(composer).toContain('<ActivityIndicator size="small" color={t.onAccent} />');
		expect(composer).not.toContain("color={t.bgBase}");
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
		for (const rule of ["removeQuote", "queueSteer", "queueDelete", "steerAction"]) {
			expect(styleRule(rule), rule).toContain("width: touchTarget, height: touchTarget");
		}
		expect(styleRule("attachmentRemove")).toContain("width: touchTarget, height: touchTarget");
		expect(source("./ComposerAttachment.tsx")).toContain('accessibilityRole="button"');
		expect(composer).toContain("hitSlop={{ top: CHIP_SLOP, bottom: CHIP_SLOP }}");
		expect(composer).toContain("const CHIP_SLOP = (touchTarget - CHIP_HEIGHT) / 2");
		expect(styleRule("deliveryNote")).toContain("height: touchTarget");
		// The corner controls are a touch target tall, and the controls row opens
		// to that height.
		expect(styleRule("corner")).toContain("height: touchTarget");
		expect(composer).toContain("height: interpolate(openness.value, [0, 1], [0, touchTarget], \"clamp\")");
		expect(styleRule("settingsSlot")).toContain("minHeight: touchTarget");
		expect(styleRule("settingsSlot")).not.toMatch(/(?<!min)[Hh]eight: /);
		expect(composer).not.toContain("hitSlop={7}");
	});

	it("brings a pushed-aside request back with a real button", () => {
		expect(composer).toContain('<Button title="Answer" variant="secondary" onPress={onRestoreRequest} />');
		expect(composer).not.toContain("textDecorationLine");
	});

	// The button alone would announce a bare "Answer", with no word of which
	// request it brings back.
	it("names the request a screen reader would bring back", () => {
		expect(composer).toContain("accessibilityLabel={`${request.title}. Answer it`}");
		expect(composer).toContain('accessibilityActions={[{ name: "activate", label: "Answer" }]}');
		expect(composer).toContain("onAccessibilityTap={onRestoreRequest}");
		expect(composer).toContain('<View importantForAccessibility="no-hide-descendants">\n\t\t\t\t\t<Button title="Answer"');
	});

	it("draws the queue dock with the shared divider and radius", () => {
		expect(styleRule("queueDock")).toContain("borderWidth: rowDividerWidth");
		expect(styleRule("queueDock")).toContain("borderRadius: radius.lg");
		expect(styleRule("queueRowDivider")).toContain("borderTopWidth: rowDividerWidth");
		expect(composer).not.toContain("hairlineWidth");
	});

	// The level is otherwise only a bar's width and colour.
	it("gives the context meter a value a screen reader can read", () => {
		expect(composer).toContain('<View accessible accessibilityRole="progressbar" accessibilityLabel="Context window" accessibilityValue={contextMeter.accessibilityValue}');
		expect(styleRule("contextText")).toContain("fontSize: type.footnote.fontSize");
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
		expect(control).toContain('alignSelf: "stretch"');
		expect(control).toContain('overflow: "hidden"');
	});
});

describe("collapsed and expanded composer", () => {
	// Collapsed, a slim pill under the session's chips; writing grows it into a
	// card with the turn settings between attach and the round button.
	it("opens on focus and stays open while anything waits to be sent or the mic is open", () => {
		expect(composer).toContain("const expanded = focused || hasDraft || attachments.length > 0 || listening || transcribing;");
		expect(composer).toContain("onFocus={() => setFocused(true)}");
		expect(composer).toContain("onBlur={() => setFocused(false)}");
		expect(composer).toContain('<Animated.View pointerEvents={expanded ? "box-none" : "none"} style={[styles.controlsRow, { paddingLeft: CORNER_CONTROL, paddingRight: CORNER_CONTROL }, controlsOpening]}>');
		expect(composer).toContain("<ChatTurnSettingsControl snapshot={snapshot}");
	});

	// Moving the field between two parents would remount it and drop the keyboard.
	it("keeps the field in one row in both shapes", () => {
		expect(composer.match(/<TextInput\n/g)).toHaveLength(1);
		expect(composer.match(/\{attachMenu\}/g)).toHaveLength(1);
		expect(composer.match(/\{roundButton\}/g)).toHaveLength(1);
	});

	// The corner controls never move: the pill grows up around them, the field is
	// let out from between them and the controls row opens under it, all on one
	// spring that Reduce Motion turns into a cut.
	it("opens on one spring around controls pinned to its corners", () => {
		expect(composer).toContain("<View style={[styles.corner, styles.cornerLeading]}>{attachMenu}</View>");
		expect(composer).toContain("<View style={[styles.corner, styles.cornerTrailing]}>");
		expect(styleRule("corner")).toContain('position: "absolute", bottom: space.xs');
		expect(composer).toContain("openness.value = reduceMotion ? withTiming(expanded ? 1 : 0, { duration: 0 }) : withSpring(expanded ? 1 : 0, COMPOSER_SPRING);");
		expect(composer).toContain("paddingLeft: interpolate(openness.value, [0, 1], [CORNER_CONTROL, space.sm])");
		// The round button changes face in place, with no transition of its own.
		expect(composer).not.toContain("ZoomIn");
	});

	// The new-task sheet's round button: stop the mic, stop the turn, send, or
	// dictate, by what the prompt holds. Dictating drops the keyboard.
	it("follows the prompt with one round button", () => {
		expect(composer).toContain('const primary = listening ? "stopDictation" : transcribing ? "transcribing" : primaryAction === "stop" ? "stopTurn" : nothingToSend && canDictate ? "dictate" : "send";');
		expect(composer).toContain('onPress={() => { if (primary === "dictate") Keyboard.dismiss(); voice.toggleLatched(); }}');
		expect(styleRule("dictating")).toContain("backgroundColor: t.red");
	});

	it("floats over the conversation, which fades out behind it", () => {
		expect(composer).toContain("<Animated.View style={[styles.dock, styles.floatingDock, { paddingBottom: restingInset }, dockRise]}>");
		expect(styleRule("floatingDock")).toContain('backgroundColor: "transparent"');
		expect(composer).toContain("<DockFade />");
	});
});

describe("composer chips", () => {
	// The session's PR, what it changed and the context meter sit over the
	// composer, open or not.
	it("shows the session's state as chips", () => {
		expect(composer).toContain("const chips = activeReviewPR || changes || contextMeter ?");
		expect(composer).toContain("const reviewPromptAvailable = Boolean(reviewPR && onOpenReview)");
		expect(composer).toContain('accessibilityHint="Opens the pull request review" onPress={onOpenReview}');
		expect(composer).toContain('accessibilityHint="Opens the changed files" onPress={changes.onOpen}');
		expect(composer).toContain("<ComposerGlass radius={CHIP_HEIGHT / 2} />");
	});

	it("names the PR by number and its deciding status", () => {
		const chip = source("./PRChip.tsx");
		expect(chip).toContain('name="git-pull-request"');
		expect(chip).toContain("#{pr.number}");
		expect(chip).toContain("prReviewPromptHeadline(pr, summary)");
	});

	// Steering stays reachable from the open card, and the note says when a
	// message will go while a turn runs.
	it("keeps the steer action and the delivery note in the card", () => {
		expect(composer).toContain('const steerAction = deliveryPresentation.showSteerAction ? <Pressable');
		expect(composer).toContain('accessibilityLabel="Steer this turn now"');
		expect(composer).toContain('{attachments.length ? "Attachments next" : "Sent after this"}');
	});
});
