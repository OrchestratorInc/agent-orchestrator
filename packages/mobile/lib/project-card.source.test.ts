import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const card = readFileSync(new URL("./project-card.tsx", import.meta.url), "utf8");
const page = readFileSync(new URL("../app/project/[id].tsx", import.meta.url), "utf8");
const projects = readFileSync(new URL("../app/(tabs)/(projects)/projects.tsx", import.meta.url), "utf8");

describe("project row", () => {
	// Matches a worker row: a divider, not a filled card with initials.
	it("is a flat row with a divider inset to its text", () => {
		expect(card).toMatch(/divider:\s*\{\s*marginLeft:\s*space\.lg,\s*height:\s*rowDividerWidth/);
		expect(card).toMatch(/body:\s*\{\s*paddingLeft:\s*space\.lg/);
		expect(card).toMatch(/row:\s*\{\s*backgroundColor:\s*t\.bgBase\s*\}/);
		expect(card).not.toContain("projectInitials");
	});

	// A round button at the row's end, play to start or resume the orchestrator
	// and the orchestrator's mark to open a running one: a chevron there read as
	// "open this row", and the row itself opens the project.
	it("ends in the orchestrator's round button, with no bar under it", () => {
		expect(card).toContain("trailing={<OrchestratorPill row={row} busy={busy} onPress={onOrchestrator} />}");
		expect(card).not.toContain("ProjectCardActions");
		expect(card).not.toContain(">Expand</Text>");
		expect(card).toContain("if (live) return <OrchestratorIcon size={GLYPH_SIZE} color={color} />;");
		expect(card).not.toContain("M9.5 5.5 16 12l-6.5 6.5");
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
	});

	// Navigating to a tab from a pushed page stacks a second board over the first;
	// going back down to the one beneath does not.
	it("returns to the Projects board beneath it rather than pushing another", () => {
		expect(page).toContain('useBoardTabBeneath("(projects)");');
		expect(page).toContain('onPress={() => router.dismissTo("/projects")}');
		expect(page).not.toContain('router.navigate("/projects")');
	});

	// Reuses the board rather than copying it, so the two cannot drift.
	it("lists workers with the Workers board, archive included", () => {
		expect(page).toContain("<WorkerBoardList");
		expect(page).toContain("projectDetailSessions(");
	});

	// Each platform's own create action, scoped to this project, at the bottom
	// right: on iOS the compose button the board's tab bar sets apart, as a bottom
	// toolbar's trailing item; on Android Material's FAB, clear of the gesture
	// bar with the list scrolling out from under it.
	it("starts a task in this project from a bottom-right compose button on iOS and a FAB on Android", () => {
		expect(page).toContain('const bottomCompose = Platform.OS === "ios" && offerNewTask ? (');
		expect(page).toMatch(/<Stack\.Toolbar placement="bottom">\s*<Stack\.Toolbar\.Spacer \/>/);
		expect(page).toContain('<Stack.Toolbar.Button icon="square.and.pencil" accessibilityLabel="New task" onPress={startTask}>New task</Stack.Toolbar.Button>');
		expect(page).not.toContain('toolbarIcon("add")');
		expect(page).toContain('<AddFab expanded={fab.expanded} label="New task" onPress={startTask} bottomInset={insets.bottom} testID="project-spawn-worker" />');
		expect(page).toContain('params: { projectId: id, hostId: routeHostId }');
	});

	// One way to start a task at a time: "No workers yet" has its own Start
	// task, so the compose button and the FAB wait for a worker to show.
	it("leaves the compose button and the FAB out while No workers yet offers Start task", () => {
		expect(page).toContain("const offerNewTask = canStartTask && !noWorkersShown;");
		expect(page).toContain("onEmptyChange={setNoWorkersShown}");
		expect(page).toContain('{offerNewTask ? <AddFab expanded={fab.expanded} label="New task"');
		expect(page).toContain('action={<Button title="Start task" icon="plus" onPress={startTask} />}');
	});
});
