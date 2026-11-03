import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const card = readFileSync(new URL("./project-card.tsx", import.meta.url), "utf8");
const page = readFileSync(new URL("../app/project/[id].tsx", import.meta.url), "utf8");
const projects = readFileSync(new URL("../app/(tabs)/(projects)/projects.tsx", import.meta.url), "utf8");

describe("project row", () => {
	// A filled card, no outline, as a Settings group is.
	it("is a filled card with no outline", () => {
		expect(card).toMatch(/card:\s*\{[^}]*borderRadius:\s*radius\.lg[^}]*backgroundColor:\s*t\.bgElevated/s);
		expect(card).not.toMatch(/card:\s*\{[^}]*borderWidth/s);
	});

	// A round button at the card's end, play to start or resume the orchestrator
	// and a chevron to open a running one. The card itself opens the project.
	it("ends in the orchestrator's round button, with no bar under it", () => {
		expect(card).toContain("trailing={<OrchestratorPill row={row} busy={busy} onPress={onOrchestrator} />}");
		expect(card).not.toContain("ProjectCardActions");
		expect(card).not.toContain(">Expand</Text>");
	});

	it("leads with the project's initials on its own colour", () => {
		expect(card).toContain("backgroundColor: projectHue(row.project.name)");
		expect(card).toContain("{projectInitials(row.project.name)}");
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

	// Each platform's own create action, scoped to this project: a header "+" on
	// iOS, which has no floating button, and Material's FAB on Android, clear of
	// the gesture bar with the list scrolling out from under it.
	it("starts a task in this project from a header + on iOS and a FAB on Android", () => {
		expect(page).toContain('const headerAdd = Platform.OS === "ios" && canStartTask;');
		expect(page).toContain('{headerAdd ? <Stack.Toolbar.Button icon={toolbarIcon("add")} accessibilityLabel="New task" onPress={startTask} /> : null}');
		expect(page).toContain('<AddFab expanded={fab.expanded} label="New task" onPress={startTask} bottomInset={insets.bottom} testID="project-spawn-worker" />');
		expect(page).toContain('params: { projectId: id, hostId: routeHostId }');
	});
});
