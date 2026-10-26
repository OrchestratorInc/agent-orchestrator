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
		expect(composer).toContain("<ComposerGlass radius={COMPOSER_RADIUS} />");
		expect(composer).not.toContain("<ComposerGlass height=");
		// The PR card measures its own header for the drag-to-collapse animation;
		// that measurement does not size the glass behind the composer row.
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

	// Two discs side by side have no hierarchy; the send button is the only shape
	// that commits, so dictation is a bare glyph here.
	it("keeps dictation a glyph and the send a filled circle", () => {
		expect(composer).toContain('<MicKey variant="plain" size={touchTarget}');
		expect(styleRule("send")).toContain("width: touchTarget, height: touchTarget, borderRadius: radius.pill");
		expect(styleRule("send")).toContain("backgroundColor: t.accent");
	});

	// The glyph on the filled accent is the accent's own ink. `bgBase` happened
	// to match it on the default themes and nowhere else.
	it("inks the send glyph for the accent fill", () => {
		expect(composer).toContain('<AppSymbol name="send" size={iconSize.md} color={t.onAccent} />');
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
		expect(composer).toContain("hitSlop={{ top: CHANGES_PILL_SLOP, bottom: CHANGES_PILL_SLOP }}");
		expect(composer).toContain("const CHANGES_PILL_SLOP = (touchTarget - CHANGES_PILL_HEIGHT) / 2");
		expect(styleRule("deliveryNote")).toContain("height: touchTarget");
		// The settings and delivery row is a row of those targets, and grows
		// rather than clip the turn-settings trigger (48dp on Android).
		expect(styleRule("metaRow")).toContain('width: "100%", minHeight: touchTarget');
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

describe("composer meta row", () => {
	const control = source("./ChatTurnSettingsControl.tsx");

	// "GPT-5.6-Sol · Medium · Ask when unsure" is the longest thing in the row, and
	// two others can join it: the queued-message note and the context meter. The
	// label was drawn straight across both — the settings control is the one that
	// yields, and it is clipped so a label that fails to truncate cannot paint over
	// its neighbours.
	it("lets the settings label yield instead of the meter", () => {
		const meta = styleRule("metaRow");
		expect(meta).toContain("gap: space.sm");
		expect(styleRule("settingsSlot")).toContain("overflow: \"hidden\"");
		expect(styleRule("contextMeter")).toContain("flexShrink: 0");
		expect(styleRule("deliveryNote")).toContain("flexShrink: 0");
		expect(control).toContain('alignSelf: "stretch"');
		expect(control).toContain('overflow: "hidden"');
	});
});

describe("PR review composer", () => {
	it("groups the PR prompt, settings selector, and message input inside one review surface", () => {
		expect(composer).toContain("const reviewPromptAvailable = Boolean(reviewPR && onOpenReview)");
		expect(composer).toContain("{showReviewPrompt ? <Animated.View style={[styles.reviewArea, styles.reviewAreaWithPrompt]}>");
		expect(composer).toContain("<Animated.View style={[styles.reviewContainer, reviewCardDragStyle]}");
		expect(composer).toContain("<PRReviewPrompt pr={activeReviewPR}");
		expect(composer).toContain("<TextInput");
		expect(composer).toContain("{activeReviewPR ? <Animated.View pointerEvents=\"none\" style={[StyleSheet.absoluteFill, reviewComposerGlassStyle]}>");
	});

	it("uses supported PR and navigation symbols instead of a blank icon tile", () => {
		const prompt = source("./PRReviewPrompt.tsx");
		expect(prompt).toContain('name="git-pull-request"');
		expect(prompt).toContain('<AppSymbol name="chevronForward"');
		expect(prompt).not.toContain('name="arrow-up-right"');
	});

	// A 30x3 bar that looked like a sheet grabber was really a 16pt collapse
	// button. It is a real one now, beside the card rather than inside it.
	it("collapses the card from a full-size chevron button", () => {
		const prompt = source("./PRReviewPrompt.tsx");
		expect(prompt).toContain('accessibilityLabel="Collapse pull request card"');
		expect(prompt).toContain('<AppSymbol name="chevronDown"');
		expect(prompt).toContain("collapse: { width: touchTarget, height: touchTarget");
		expect(prompt).not.toContain("dragHandle");
	});

	it("folds the card on a UI-thread drag that honours Reduce Motion", () => {
		expect(composer).not.toContain("PanResponder");
		expect(composer).toContain("<GestureDetector gesture={reviewCardPan}>");
		expect(composer).toContain("Gesture.Pan()");
		expect(composer).toContain("const reviewCollapseMs = reduceMotion ? 0 : REVIEW_COLLAPSE_MS;");
		expect(composer).toContain("duration: reduceMotion ? 0 : REVIEW_EXPAND_MS");
	});
});

describe("session changes pill", () => {
	// Codex-style: once the session has changed files, the meta row offers them
	// where the "Sent after this" note sat. The placeholder still says a message
	// waits for the turn, so nothing is lost, and steering stays reachable.
	it("replaces the queue note and keeps the steer action", () => {
		const start = composer.indexOf("{changes && !(deliveryPresentation.showQueueNote && attachments.length)");
		expect(start).toBeGreaterThan(-1);
		const pill = composer.slice(start, composer.indexOf(': deliveryPresentation.showQueueNote ? <View style={styles.deliveryNote}>', start));
		expect(pill).toContain("onPress={changes.onOpen}");
		expect(pill).toContain("{steerAction}");
		expect(composer).toContain('const steerAction = deliveryPresentation.showSteerAction ? <Pressable');
		expect(composer).toContain('accessibilityLabel="Steer this turn now"');
		expect(composer).toContain('{attachments.length ? "Attachments next" : "Sent after this"}');
	});
});
