import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const card = readFileSync(new URL("./project-card.tsx", import.meta.url), "utf8");
const page = readFileSync(new URL("../app/project/[id].tsx", import.meta.url), "utf8");
const projects = readFileSync(new URL("../app/(tabs)/(projects)/projects.tsx", import.meta.url), "utf8");

describe("project row", () => {
	// Matches a worker row: a divider, not a bordered card.
	it("is a flat row with a divider", () => {
		expect(card).toMatch(/row:\s*\{\s*borderBottomWidth:\s*rowDividerWidth/);
		expect(card).toMatch(/body:\s*\{\s*paddingLeft:\s*space\.lg/);
	});

	// The full-width button outweighed the project name; a compact pill does
	// not, and is still the only solid control on the row. It is a capsule in a
	// trailing slot that takes its own width, so a large text size narrows the
	// body rather than running it under a pill laid over a fixed reservation.
	it("carries the orchestrator as a capsule pill in a trailing slot", () => {
		expect(card).toContain("trailing={<OrchestratorPill");
		expect(card).toContain("<OrchestratorIcon");
		expect(card).toContain("copy.short");
		expect(card).toMatch(/pill:\s*\{[^}]*minHeight:\s*PILL_HEIGHT[^}]*borderRadius:\s*radius\.pill/s);
		expect(card).toMatch(/trailingSlot:\s*\{\s*flexShrink:\s*0/);
		expect(card).not.toContain('position: "absolute"');
		expect(card).not.toMatch(/paddingRight:\s*1[25]0/);
	});

	// iOS highlights a row and fades a button; Android ripples both.
	it("answers a press the way each platform does", () => {
		expect(card).toContain('usePressFeedback("row")');
		expect(card).toContain("android_ripple={feedback.android_ripple}");
		expect(card).not.toContain("opacity: 0.8");
		expect(card).not.toContain("<ActivityIndicator");
	});

	it("keeps counts as quiet text rather than chips", () => {
		expect(card).toContain("projectCardSummary(row)");
		expect(card).not.toContain("<Chip");
		expect(card).not.toContain('<Feather name="folder"');
	});

	it("opens the project from the row and the orchestrator from the pill", () => {
		expect(card).toContain("onPress={() => onOpenProject(row)}");
		expect(projects).toContain('pathname: "/project/[id]"');
		expect(projects).toContain("onOrchestrator={openOrchestrator}");
	});

	// Opening a page is navigation, not a moment worth a buzz.
	it("opens a project without a haptic", () => {
		expect(projects).not.toContain("haptics.select()");
	});
});

describe("project page", () => {
	it("puts the orchestrator on top of the project's workers", () => {
		expect(page).toContain("ListHeaderComponent={");
		expect(page).toContain("<ProjectPageHeader");
		expect(card).toContain("styles.stats");
	});

	// The native header carries the project's name, and so does the back-history
	// menu of a session opened from here.
	it("titles the native header with the project's name", () => {
		expect(page).toContain('const title = row?.project.name ?? "Project";');
		expect(page).toContain("navigation.setOptions({ title })");
		expect(page).not.toContain("ScreenHeader");
		expect(page).not.toContain("insets.top");
	});

	// The system back button, not a drawn one. A deep link opens this page over
	// a board still on Workers, so the page turns the board to Projects beneath
	// it, and every way back lands there.
	it("goes back to Projects with the system back button", () => {
		expect(page).toContain('useBoardTabBeneath("(projects)");');
		expect(page).not.toContain("HeaderIconButton");
		expect(page).not.toContain("dismissTo(");
		expect(page).not.toContain("backOr(");
	});

	it("shows its machine as a trailing header item", () => {
		expect(page).toContain('<Stack.Toolbar placement="right">');
		expect(page).toMatch(/<Stack\.Toolbar\.View hidesSharedBackground>\s*<ProjectMachineIndicator hostId=\{currentHostId\} hosts=\{hostStates\} \/>/);
	});

	// Reuses the board rather than copying it, so the two cannot drift.
	it("lists workers with the Workers board, archive included", () => {
		expect(page).toContain("<WorkerBoardList");
		expect(page).toContain("projectDetailSessions(");
	});

	// Each platform's own create action, scoped to this project: a header "+" on
	// iOS, which has no floating button, and Material's FAB on Android, clear of
	// the gesture bar with the list scrolling out from under it.
	it("starts a task in this project from a header + on iOS and a FAB on Android", () => {
		expect(page).toContain('const headerAdd = Platform.OS === "ios" && canStartTask;');
		expect(page).toContain('{headerAdd ? <Stack.Toolbar.Button icon={toolbarIcon("add")} accessibilityLabel="Spawn worker" onPress={startTask} /> : null}');
		expect(page).toContain('<AddFab label="Spawn worker" onPress={startTask} bottomInset={insets.bottom} testID="project-spawn-worker" />');
		expect(page).toContain('params: { projectId: id, hostId: routeHostId }');
		expect(page).toContain("contentBottomInset={insets.bottom + addFabClearance + space.xxl}");
	});
});
