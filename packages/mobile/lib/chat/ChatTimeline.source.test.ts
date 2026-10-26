import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("./ChatTimeline.tsx", import.meta.url), "utf8");
const turnSummarySource = source.slice(
	source.indexOf("function TurnStatusRow"),
	source.indexOf("function TurnPlan"),
);

describe("chat turn summary styling", () => {
	it("shows the duration without a trailing divider line", () => {
		expect(turnSummarySource).toContain("Worked for ${duration}");
		expect(turnSummarySource).not.toContain("<View style={styles.ruleHalf} />");
	});

	it("folds a settled turn's work behind the Worked for row and keeps its state above the list", () => {
		expect(source).toContain("partitionTurnItems(items, group.turn)");
		expect(source).toContain("const rowState = useMemo(() => ({ openWorked, edits, editDraft }), [editDraft, edits, openWorked]);");
		expect(source).toContain("extraData={rowState}");
		expect(turnSummarySource).toContain("accessibilityState={foldable ? { expanded: open } : undefined}");
	});

	// A turning Lucide loader only imitated a progress indicator; the system's
	// own is what each platform reads as working.
	it("shows the system activity indicator while a turn runs", () => {
		expect(turnSummarySource).toContain("{running ? <StatusSpinner><Spinner size={iconSize.xs} color={t.textTertiary} /></StatusSpinner> : null}");
		expect(turnSummarySource).not.toContain('name="loader"');
	});
});

describe("streaming response layout", () => {
	it("keeps the live cursor inside the response instead of adding a new block", () => {
		expect(source).toContain('showCursor ? `${item.text || ""} ▍`');
		expect(source).not.toContain("styles.streamingDot");
	});

	it("draws the cursor on the turn's newest reply only", () => {
		expect(source).toContain("streamingCursorMessageId(items)");
		expect(source).toContain("showCursor={row.items[0].id === cursorMessageId}");
		expect(source).not.toContain('item.streaming ? `${item.text || ""} ▍`');
	});
});

describe("empty conversation", () => {
	it("uses the AO mascot instead of a generic chat icon", () => {
		const emptyConversation = source.slice(source.indexOf("function EmptyConversation"), source.indexOf("function LabelValue"));
		expect(emptyConversation).toContain("<MascotLamp size={48} />");
		expect(emptyConversation).not.toContain('name="message-circle"');
	});
});

describe("elicitation typography", () => {
	it("matches the question to regular assistant copy", () => {
		// The family is required next to every weight now, so this pins the parts
		// that carry the intent: the question reads at assistant-copy size and weight.
		expect(source).toContain('fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "500" }');
		expect(source).toContain("inputRequest: { marginVertical: space.md, paddingHorizontal: space.hair, paddingTop: space.xs, paddingBottom: space.xl, gap: space.md }");
		expect(source).toContain('inputActions: { minHeight: 36, flexDirection: "row"');
	});
});

describe("sent human message actions", () => {
	const human = source.slice(source.indexOf('if (item.role === "user" && item.origin === "human") {'), source.indexOf('if (item.role === "user") {'));

	it("offers Edit and Copy from a long-press menu instead of an always-visible pen", () => {
		expect(human).toContain("<MessageContextMenu");
		expect(human).toContain("canEdit={Boolean(onEdit)}");
		expect(human).toContain("Clipboard.setStringAsync(editableMessageText(item.text).body.trim()); haptics.success();");
		expect(human).not.toContain('name="edit-2"');
	});

	it("keeps the bubble text unselectable so the long-press opens the menu", () => {
		expect(human).toContain("<Text style={styles.userText}>{body}</Text>");
		expect(human).not.toContain("<Text selectable");
	});
});

describe("response copy control", () => {
	const control = source.slice(source.indexOf("function CopyResponseButton"), source.indexOf("function SystemSignal"));

	// A 12pt faint glyph in a 28pt box was nearly invisible and under the
	// minimum target on both platforms.
	it("is a visible system glyph in a touch-target-sized button", () => {
		expect(control).toContain('accessibilityLabel="Copy response"');
		expect(control).toContain('<AppSymbol name={copied ? "checkmark" : "copy"} size={iconSize.md} color={copied ? t.green : t.textTertiary} />');
		expect(source).toMatch(/copy: \{[^}]*width: touchTarget, height: touchTarget/);
		expect(control).not.toContain("t.textFaint");
	});
});

describe("jump to latest", () => {
	// Each platform's own floating control (see JumpToLatest), not a bordered pill.
	it("floats the native control over the list", () => {
		expect(source).toContain("{showJump ? <JumpToLatest onPress={() => { followsTail.current = true; setShowJump(false); scrollToLatest(true); }} /> : null}");
		expect(source).not.toContain("styles.jump");
	});
});

describe("turn rollback", () => {
	const summary = source.slice(source.indexOf("function TurnSummary"), source.indexOf("function TurnPlan"));

	// Forgetting a turn cannot be undone, so it asks through the system's own
	// destructive confirmation rather than a card drawn into the timeline.
	it("confirms with a native destructive alert", () => {
		expect(summary).toContain("Alert.alert(");
		expect(summary).toContain('{ text: "Cancel", style: "cancel" }');
		expect(summary).toContain('{ text: "Roll back", style: "destructive", onPress: () => {');
		expect(summary).not.toContain("rollbackConfirm");
		expect(summary).not.toContain("setConfirming");
	});

	it("is a labelled button the size of a touch target", () => {
		expect(summary).toContain('accessibilityRole="button"');
		expect(summary).toContain('accessibilityLabel="Roll back to before this turn"');
		expect(source).toMatch(/rollback: \{ width: touchTarget, height: touchTarget/);
		expect(summary).not.toContain("hitSlop");
	});
});

describe("elicitation boolean input", () => {
	const field = source.slice(source.indexOf("function InputField"), source.indexOf("function CompactionMarker"));

	// React Native's Android switch is the older SwitchCompat, beside M3 controls
	// everywhere else in the flow.
	it("hosts the universal native switch", () => {
		expect(source).toContain('import { Host, Switch } from "@expo/ui";');
		expect(source).not.toMatch(/import \{[^}]*\bSwitch\b[^}]*\} from "react-native"/);
		expect(field).toContain("<Switch value={checked} onValueChange={onChange} />");
	});

	it("reads as one labelled switch that a screen reader can toggle", () => {
		expect(field).toContain('accessibilityRole="switch"');
		expect(field).toContain("accessibilityState={{ checked }}");
		expect(field).toContain('if (event.nativeEvent.actionName === "activate") onChange(!checked);');
	});
});

describe("disclosure rows", () => {
	// Opening a fold or a page is not a haptic moment on either platform.
	it("toggle and navigate without a haptic", () => {
		expect(source).not.toContain("haptics.tap()");
		expect(source).not.toContain("haptics.select()");
	});

	it("draw the system's disclosure chevron", () => {
		expect(source).not.toMatch(/<Feather name=\{[^}]*chevron/);
		expect(source.match(/<FoldChevron open=/g)?.length).toBeGreaterThanOrEqual(12);
	});
});

describe("message edit list identity", () => {
	it("keeps the editable sets' identity while their contents are unchanged", () => {
		expect(source).toContain("if (!sameEditableTurns(editsRef.current, nextEdits)) editsRef.current = nextEdits;");
		expect(source).toContain("const rowState = useMemo(() => ({ openWorked, edits, editDraft }), [editDraft, edits, openWorked]);");
	});
});

