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

	it("holds the working indicator still under Reduce Motion", () => {
		expect(turnSummarySource).toContain('<Spinning enabled><Feather name="loader"');
		expect(turnSummarySource).not.toContain("<ActivityIndicator");
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
		const emptyConversation = source.slice(source.indexOf("function EmptyConversation"), source.indexOf("function Action"));
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
		// Copy takes what the user typed, without the worker reports folded into the message.
		expect(human).toMatch(/Clipboard\.setStringAsync\(\s*editableMessageText\(\s*humanVisibleText\(\s*item\.text\s*\)\s*\)\.body\.trim\(\)\s*\)/);
		expect(human).not.toContain('name="edit-2"');
	});

	it("keeps the bubble text unselectable so the long-press opens the menu", () => {
		expect(human).toContain("<Text style={styles.userText}>{body}</Text>");
		expect(human).not.toContain("<Text selectable");
	});
});

describe("message edit list identity", () => {
	it("keeps the editable sets' identity while their contents are unchanged", () => {
		expect(source).toContain("if (!sameEditableTurns(editsRef.current, nextEdits)) editsRef.current = nextEdits;");
		expect(source).toContain("const rowState = useMemo(() => ({ openWorked, edits, editDraft }), [editDraft, edits, openWorked]);");
	});
});

