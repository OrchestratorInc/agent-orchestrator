import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("./ChatTimeline.tsx", import.meta.url), "utf8");
const fieldSource = readFileSync(new URL("./ElicitationInputField.tsx", import.meta.url), "utf8");
const turnSummarySource = source.slice(
	source.indexOf("function TurnStatusRow"),
	source.indexOf("function ChangedFiles"),
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

	it("draws the desktop's lattice as the working indicator", () => {
		expect(turnSummarySource).toContain("<LatticeLoader color={t.textTertiary} />");
		expect(turnSummarySource).not.toContain('<Feather name="loader"');
		expect(turnSummarySource).not.toContain("<ActivityIndicator");
	});

	it("shows only the ticking timer while running, like the desktop, and still says Working to screen readers", () => {
		expect(turnSummarySource).toContain(': workingDuration ?? "";');
		expect(turnSummarySource).not.toContain("`Working · ${workingDuration}`");
		expect(turnSummarySource).toContain("`Working for ${workingDuration}`");
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
		expect(fieldSource).toContain('fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "500" }');
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
		expect(source).toContain("const jumpToLatest = () => { followsTail.current = true; setShowJump(false); scrollToLatest(true); };");
		// Floating above what sits over the list's end, so it never moves the
		// composer or the conversation, and fading rather than popping.
		// It steps aside while the composer is open.
		expect(source).toContain('{showJump && !jumpHidden ? <Animated.View entering={FadeIn.duration(JUMP_FADE_MS)} exiting={FadeOut.duration(JUMP_FADE_MS)} pointerEvents="box-none" style={[StyleSheet.absoluteFill, { bottom: bottomInset }]}>');
		const screen = readFileSync(new URL("./ChatSessionScreen.tsx", import.meta.url), "utf8");
		expect(screen).toContain("jumpHidden={composerOpen}");
		expect(screen).toContain("onExpandedChange={setComposerOpen}");
		expect(source).toContain("<JumpToLatest onPress={jumpToLatest} />");
		expect(source).not.toContain("styles.jump");
	});
});

// TurnRollbackButton.test and ElicitationInputField.test render these controls.
describe("turn rollback", () => {
	const summary = source.slice(source.indexOf("function TurnSummary"), source.indexOf("function TurnPlan"));

	// Forgetting a turn cannot be undone, so the control asks through the
	// system's destructive alert rather than a card drawn into the timeline.
	it("offers the confirming control on a settled turn the provider knows", () => {
		expect(summary).toContain("const canRollBack = settled && Boolean(turn.providerTurnId) && !turn.rolledBack;");
		expect(summary).toContain("{onRollback && canRollBack ? <View style={styles.turnLine}><TurnRollbackButton turnId={turn.id} onRollback={onRollback} /></View> : null}");
		expect(summary).not.toContain("rollbackConfirm");
		expect(summary).not.toContain("setConfirming");
	});
});

describe("elicitation boolean input", () => {
	// React Native's Android switch is the older SwitchCompat, beside M3 controls
	// everywhere else in the flow.
	it("hosts the universal native switch", () => {
		expect(source).toContain("<ElicitationInputField key={name}");
		expect(fieldSource).toContain('import { Host, Switch } from "@expo/ui";');
		for (const file of [source, fieldSource]) expect(file).not.toMatch(/import \{[^}]*\bSwitch\b[^}]*\} from "react-native"/);
	});
});

describe("request cards in the timeline", () => {
	const approval = source.slice(source.indexOf("function ApprovalCard"), source.indexOf("function UserInputCard"));
	const input = source.slice(source.indexOf("function UserInputCard"), source.indexOf("function CompactionMarker"));

	// As in the composer's RequestCard: the press tapped and nothing said whether
	// the answer landed. Now the outcome is the one haptic.
	it("buzz once, when an answer lands or fails", () => {
		expect(approval).toMatch(/onDecide\(activity\.requestId \?\? "", decision\.id\)\s*\.then\(\(\) => haptics\.success\(\)\)/);
		expect(approval).toMatch(/setSubmitError\(userFacingError\(cause\)\);\s*haptics\.error\(\);/);
		expect(approval).toContain("primary={index === 0} reportsOutcome");
		expect(input).toMatch(/await onResolve\(activity\.requestId, action, content\);\s*haptics\.success\(\);/);
		expect(input).toMatch(/setSubmitError\(userFacingError\(cause\)\);\s*haptics\.error\(\);/);
		for (const label of ['label="Cancel" reportsOutcome', '"Decline" : "Skip"} reportsOutcome', '"Open link"} primary reportsOutcome', "step.primaryLabel} primary reportsOutcome"]) {
			expect(input).toContain(label);
		}
	});

	// Advancing to the next question is silent, so going back must be too.
	it("step between questions without a haptic either way", () => {
		expect(input).toContain('label="Back" reportsOutcome');
		expect(input).toMatch(/if \(hasNextQuestion\) \{\s*setActiveQuestion\(\(current\) => current \+ 1\);\s*return;\s*\}/);
	});

	// The button sizes itself to its label; a width guessed from the label's
	// length only cut long decisions short.
	it("let each decision size itself", () => {
		expect(approval).not.toContain("width=");
		expect(source).not.toContain("actionControlWidth");
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
		expect(source.match(/<FoldChevron open=/g)?.length).toBeGreaterThanOrEqual(11);
	});
});

describe("letter case", () => {
	// Neither HIG nor Material sets labels in tracked capitals; they read as shouting.
	it("labels in sentence case", () => {
		for (const label of ["STEERED", "AGENT TYPED", "SUBAGENT", "HISTORY COMPACTED", "STILL CHANGING", "GROWING", "PROGRESS", "FULL"]) expect(source).not.toContain(label);
		for (const label of [">Steered<", ">Agent typed<", "Subagent · {count}", "History compacted{", ">Growing<", ">Progress<"]) expect(source).toContain(label);
		expect(source).not.toContain("{label.toUpperCase()}");
		expect(source).not.toContain('textTransform: "uppercase"');
		expect(source).not.toContain("letterSpacing");
	});
});

describe("card shape", () => {
	const styles = source.slice(source.indexOf("const makeStyles"));

	// r8 with 1px, r12 with a hairline and r16 had grown up side by side.
	it("gives every card one radius and one edge width", () => {
		expect(styles).toContain('const card = { borderRadius: radius.md, borderCurve: "continuous", borderWidth: rowDividerWidth } as const;');
		for (const name of ["systemSignal", "runDetail", "planCard", "requestCard", "errorCard"]) expect(styles).toMatch(new RegExp(`\\n\\t+${name}: \\{ \\.\\.\\.card,`));
		expect(styles).not.toMatch(/borderRadius: \d/);
		// The origin message's 2pt bar is a quote mark, not an edge.
		expect(styles).not.toMatch(/[bB]order(?!LeftWidth: 2)\w*Width: \d/);
		expect(styles).not.toContain("hairlineWidth");
	});
});

describe("timeline rows", () => {
	const styles = source.slice(source.indexOf("const makeStyles"));

	it("are touch targets that press the platform's way", () => {
		for (const row of ["activityRow", "runSummary", "subagentHeader", "planHeader", "fileRowTap", "originMore"]) expect(styles).toMatch(new RegExp(`\\n\\t+${row}: \\{[^}]*minHeight: touchTarget`));
		expect(source.match(/usePressFeedback\("row"\)/g)?.length).toBeGreaterThanOrEqual(10);
		expect(source).not.toContain("press.opacity");
		expect(styles).toMatch(/\n\t+statusRow: \{ minHeight: touchTarget,/);
		expect(source).not.toContain("STATUS_ROW_HEIGHT");
	});

	it("show work in progress with the system spinner", () => {
		expect(source).not.toContain("<ActivityIndicator");
	});

	// Retry is a control, so it takes the system's glyph rather than a timeline one.
	it("retry a failed attachment under the system refresh glyph", () => {
		const attachment = source.slice(source.indexOf("function StagedAttachment"), source.indexOf("function deliveryCopy"));
		expect(attachment).toContain('<AppSymbol name="refresh" size={iconSize.xs} color={t.textTertiary} />');
		expect(attachment).not.toContain("refresh-cw");
	});

	it("set nothing below 13pt", () => {
		expect(styles).not.toContain("type.caption2");
		expect(styles).not.toContain("type.caption1");
		expect(styles).toMatch(/turnState: \{[^}]*type\.footnote\.fontSize, lineHeight: type\.footnote\.lineHeight/);
	});

	// The status row takes the startup checklist header's place, so the two are
	// one size and the handoff does not jump.
	it("match the startup checklist header they hand off to", () => {
		const startup = readFileSync(new URL("./SessionStartup.tsx", import.meta.url), "utf8");
		expect(startup).toMatch(/\n\theader: \{ minHeight: touchTarget,/);
		expect(startup).toMatch(/headerLabel: \{[^}]*type\.footnote\.fontSize, lineHeight: type\.footnote\.lineHeight/);
		expect(styles).toMatch(/\n\t+statusRow: \{ minHeight: touchTarget,[^}]*paddingVertical: space\.xs, borderBottomWidth: rowDividerWidth/);
		expect(startup).toMatch(/\n\theader: \{[^}]*paddingVertical: space\.xs, borderBottomWidth: rowDividerWidth/);
	});

	// The bullet's column is one line of the path at the user's text size, so the
	// bullet stays level with the path's first line as the text grows.
	it("draw a file list's bullets beside the selectable path", () => {
		const fileList = source.slice(source.indexOf("function FileList"), source.indexOf("function printable"));
		expect(fileList).toContain("<FirstLineMarker lineStyle={styles.fileLine} style={styles.fileBulletColumn}><View style={styles.fileBullet} /></FirstLineMarker>");
		expect(fileList).not.toContain("•");
		expect(styles).toContain("filePath: { flex: 1, color: t.textSecondary, ...fileLine },");
		expect(styles).not.toMatch(/fileBullet: \{[^}]*marginTop/);
	});
});

describe("state in words", () => {
	it("names a failed command or tool call instead of only turning it red", () => {
		const command = source.slice(source.indexOf("function GenericActivityRow"), source.indexOf("function TerminalInput"));
		const tool = source.slice(source.indexOf("function McpToolRow"), source.indexOf("function JsonPayload"));
		expect(command).toContain('{activity.status === "failed" ? <Text style={styles.runFailed}>failed</Text> : null}');
		expect(tool).toContain('{failed && activity.status !== "running" ? <Text style={styles.runFailed}>failed</Text> : null}');
	});

	it("heads the empty conversation", () => {
		expect(source).toContain('<Text accessibilityRole="header" style={styles.emptyTitle}>');
	});
});

describe("message edit list identity", () => {
	it("keeps the editable sets' identity while their contents are unchanged", () => {
		expect(source).toContain("if (!sameEditableTurns(editsRef.current, nextEdits)) editsRef.current = nextEdits;");
		expect(source).toContain("const rowState = useMemo(() => ({ openWorked, edits, editDraft }), [editDraft, edits, openWorked]);");
	});
});


describe("following the newest message", () => {
	it("opens on the newest rows instead of drawing the oldest and jumping to an undrawn end", () => {
		expect(source).toContain("openedAt.current ??= openingRow(groups.length, OPENING_ROWS);");
		expect(source).toContain("initialScrollIndex={initialScrollIndex > 0 ? initialScrollIndex : undefined}");
		expect(source).toContain("if (index === initialScrollIndex && followsTail.current) {");
	});

	it("stops following on the person's own drag and resumes only at rest at the end", () => {
		expect(source).toContain("onScrollBeginDrag={() => { dragging.current = true; followsTail.current = false; }}");
		expect(source).toContain("onScrollEndDrag={settle}");
		expect(source).toContain("onMomentumScrollEnd={settle}");
		expect(source).toContain("if (tail && !dragging.current) followsTail.current = true;");
		// A scroll event alone never turns the follow off: rows drawn above change
		// the content's size mid-drag without the person having left the end.
		expect(source).not.toMatch(/followsTail\.current = contentSize\.height/);
		expect(source).toContain("if (followsTail.current) requestAnimationFrame(() => scrollToLatest(false));");
	});
});

describe("plan activity row", () => {
	const planActivity = source.slice(source.indexOf("function PlanActivity"), source.indexOf("function ActivityRun"));

	it("draws the plan as the desktop does, without the summary as a title or a live state", () => {
		// The summary repeats the count and current step ("Plan 3/7: <step>"), and the
		// row's status is "running" while any step is unfinished, not while the turn runs.
		expect(planActivity).toContain("<TurnPlan plan={{ explanation: activity.detail?.explanation, steps }} />");
		expect(planActivity).not.toMatch(/<TurnPlan[^>]*\b(title|live)=/);
	});
});
