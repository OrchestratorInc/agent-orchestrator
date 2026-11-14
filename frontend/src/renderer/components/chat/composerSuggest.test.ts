import { describe, expect, it } from "vitest";
import {
	findActiveTrigger,
	moveHighlight,
	rankFiles,
	rankSkills,
} from "./composerSuggest";
import type { ChatSkill } from "../../types/conversation";

const skill = (name: string, description = "", source = "user"): ChatSkill => ({
	name,
	displayName: name,
	description,
	source,
});

describe("findActiveTrigger", () => {
	it("opens a skill trigger for a slash at the start of the message", () => {
		expect(findActiveTrigger("/rev", 4)).toEqual({ kind: "skill", start: 0, query: "rev" });
	});

	it("opens a skill trigger for a bare slash, so the whole catalog is browsable", () => {
		expect(findActiveTrigger("/", 1)).toEqual({ kind: "skill", start: 0, query: "" });
	});

	it("opens a slash command after existing prose", () => {
		expect(findActiveTrigger("read src/app", 12)).toBeUndefined();
		expect(findActiveTrigger("either and/or", 13)).toBeUndefined();
		expect(findActiveTrigger("look at /rev", 12)).toEqual({
			kind: "skill",
			start: 8,
			query: "rev",
		});
	});

	it("opens a file trigger for an at-sign after whitespace", () => {
		expect(findActiveTrigger("look at @Chat", 13)).toEqual({
			kind: "file",
			start: 8,
			query: "Chat",
		});
	});

	// Otherwise an email address opens a file menu mid-word.
	it("ignores an at-sign inside a word", () => {
		expect(findActiveTrigger("mail dhruv@corethink.ai", 23)).toBeUndefined();
	});

	// Once there is a space the user has moved on to prose; holding the menu open
	// would keep Enter hijacked.
	it("closes the trigger at whitespace", () => {
		expect(findActiveTrigger("@src/app more words", 19)).toBeUndefined();
		expect(findActiveTrigger("/review now", 11)).toBeUndefined();
	});

	it("reads the query up to the caret, not to the end of the text", () => {
		expect(findActiveTrigger("@ChatComposer.tsx", 5)).toEqual({
			kind: "file",
			start: 0,
			query: "Chat",
		});
	});

	it("finds nothing in plain text", () => {
		expect(findActiveTrigger("just a message", 14)).toBeUndefined();
		expect(findActiveTrigger("", 0)).toBeUndefined();
	});
});

describe("rankSkills", () => {
	const skills = [
		skill("code-review", "Review a pull request"),
		skill("review", "Look over the diff"),
		skill("ship", "Open a PR"),
		skill("investigate", "Root cause analysis for a bug"),
	];

	it("returns everything for an empty query so the catalog is browsable", () => {
		expect(rankSkills(skills, "").map((item) => item.value)).toHaveLength(4);
	});

	// An exact/prefix match on the name has to beat a match buried mid-name,
	// otherwise the ordering reads as arbitrary.
	it("ranks a name prefix above a mid-name match", () => {
		const ranked = rankSkills(skills, "rev").map((item) => item.value);
		expect(ranked[0]).toBe("review");
		expect(ranked).toContain("code-review");
		expect(ranked.indexOf("review")).toBeLessThan(ranked.indexOf("code-review"));
	});

	it("ignores hyphens, underscores and spaces in the query and the name", () => {
		const listed = [
			skill("run-ao-fast", "Launch the app"),
			skill("ce-plan", "Plan the work"),
			skill("explanation", "Explain a change"),
		];
		const ranked = (query: string) => rankSkills(listed, query).map((item) => item.value);
		expect(ranked("runaofast")).toEqual(["run-ao-fast"]);
		expect(ranked("run-ao-fast")).toEqual(["run-ao-fast"]);
		expect(ranked("run_ao")).toEqual(["run-ao-fast"]);
		expect(ranked("ceplan")).toEqual(ranked("ce-plan"));
		// A match at the start of a word still outranks one inside a word.
		expect(ranked("plan")).toEqual(["ce-plan", "explanation"]);
	});

	it("matches on the description so a half-remembered skill is still findable", () => {
		expect(rankSkills(skills, "root cause").map((item) => item.value)).toEqual(["investigate"]);
	});

	it("drops non-matches entirely", () => {
		expect(rankSkills(skills, "zzzz")).toEqual([]);
	});

	it("is empty when the provider reported no skills", () => {
		expect(rankSkills([], "rev")).toEqual([]);
		expect(rankSkills([], "")).toEqual([]);
	});

	it("carries the label, first description line, and scope onto the row", () => {
		const [row] = rankSkills([skill("ship", "Open a PR\nsecond line", "repo")], "ship");
		expect(row).toMatchObject({
			value: "ship",
			label: "Ship",
			detail: "Open a PR",
			badge: "repo",
		});
	});

	it("shows and searches the provider's command argument hint", () => {
		const command = { ...skill("review", "Review a change"), inputHint: "<pull-request>" };
		const [row] = rankSkills([command], "pull-request");
		expect(row).toMatchObject({
			value: "review",
			detail: "Review a change · <pull-request>",
		});
	});

	// The `skill:` prefix is how a Pi-style agent runs the skill, so the row shows
	// the name alone while a pick still inserts the full command.
	it("shows a skill command by its name but keeps the command as the value", () => {
		const listed = [
			skill("skill:Better Interface", "Cross-discipline interface review", "agent"),
			skill("skill:blast-radius", "Find what a change could break", "agent"),
		];
		expect(rankSkills(listed, "")).toEqual([
			expect.objectContaining({ value: "skill:Better Interface", label: "Better Interface" }),
			expect.objectContaining({ value: "skill:blast-radius", label: "Blast Radius" }),
		]);
		expect(rankSkills(listed, "better").map((item) => item.value)).toEqual(["skill:Better Interface"]);
		expect(rankSkills(listed, "skill:blast").map((item) => item.value)).toEqual(["skill:blast-radius"]);
	});

	it("shows every agent's entries by name alone", () => {
		const listed = [skill("review", "", "agent"), skill("compact", "", "AO"), skill("skill:ship", "", "agent")];
		expect(Object.fromEntries(rankSkills(listed, "").map((item) => [item.value, item.label]))).toEqual({
			review: "Review",
			compact: "Compact",
			"skill:ship": "Ship",
		});
	});

	// Most skills carry only a slug, so the row reads it as a title. The slug is
	// still what a pick inserts.
	it("reads a slug as a title and keeps a label the skill gave", () => {
		const listed = [
			skill("ce-plan", "", "agent"),
			skill("better-ui", "", "agent"),
			skill("__remote-workflow", "", "agent"),
			skill("grill-me", "", "agent"),
			skill("ios-qa", "", "agent"),
			{ ...skill("better-interface", "", "agent"), displayName: "Better Interface" },
		];
		expect(Object.fromEntries(rankSkills(listed, "").map((item) => [item.value, item.label]))).toEqual({
			"ce-plan": "CE Plan",
			"better-ui": "Better UI",
			"__remote-workflow": "Remote Workflow",
			"grill-me": "Grill Me",
			"ios-qa": "iOS QA",
			"better-interface": "Better Interface",
		});
	});

	it("titles a slug that names an Object property as a word", () => {
		const [row] = rankSkills([skill("constructor", "", "agent")], "");
		expect(row?.label).toBe("Constructor");
	});

	it("shows a name with nothing after its namespace whole rather than blank", () => {
		const [row] = rankSkills([skill("plugin:", "", "agent")], "");
		expect(row?.label).toBe("plugin:");
	});

	it("names a plugin skill without its namespace, which shows when two read the same", () => {
		const listed = [skill("ce-plan", "", "agent"), skill("compound-engineering:ce-plan", "", "agent")];
		expect(rankSkills(listed, "")).toEqual([
			expect.objectContaining({ value: "ce-plan", label: "CE Plan", badge: "agent" }),
			expect.objectContaining({ value: "compound-engineering:ce-plan", label: "CE Plan", badge: "compound-engineering" }),
		]);
	});

	it("cuts a description to its first sentence and drops Claude's source tags", () => {
		const listed = [
			skill("ce-ideate", "Generate and evaluate grounded ideas. Use when the user asks for ideas.", "agent"),
			skill("gstack", "Router for the gstack skill suite. (gstack) (user)", "agent"),
			skill("promote", "Draft launch copy for a shipped feature (user)", "agent"),
			skill("example", "Check e.g. this file. Then stop.", "agent"),
			skill("export", "Export reports (PDF) (user)", "agent"),
		];
		expect(Object.fromEntries(rankSkills(listed, "").map((item) => [item.value, item.detail]))).toEqual({
			"ce-ideate": "Generate and evaluate grounded ideas",
			gstack: "Router for the gstack skill suite",
			promote: "Draft launch copy for a shipped feature",
			example: "Check e.g. this file",
			export: "Export reports (PDF)",
		});
	});

	it("names the namespace quietly when two skills would read the same", () => {
		const listed = [
			{ ...skill("design:polish", "", "user"), displayName: "Polish" },
			{ ...skill("writing:polish", "", "user"), displayName: "Polish" },
			{ ...skill("tidy", "", "user"), displayName: "Tidy" },
		];
		expect(rankSkills(listed, "")).toEqual([
			expect.objectContaining({ value: "design:polish", label: "Polish", badge: "design" }),
			expect.objectContaining({ value: "tidy", label: "Tidy", badge: "user" }),
			expect.objectContaining({ value: "writing:polish", label: "Polish", badge: "writing" }),
		]);
	});

	it("orders equal scores by name so the list does not reshuffle on refetch", () => {
		const a = rankSkills([skill("beta"), skill("alpha")], "");
		expect(a.map((item) => item.value)).toEqual(["alpha", "beta"]);
	});
});

describe("rankFiles", () => {
	const paths = [
		"AGENTS.md",
		"backend/internal/ports/chat.go",
		"frontend/src/renderer/components/chat/ChatComposer.tsx",
		"frontend/src/renderer/components/chat/ChatWorkspace.tsx",
	];

	// People search for the file name, not the directories above it.
	it("scores the basename ahead of the full path", () => {
		const ranked = rankFiles(paths, "chatcomposer");
		expect(ranked[0]?.value).toBe(
			"frontend/src/renderer/components/chat/ChatComposer.tsx",
		);
	});

	it("narrows on a directory fragment", () => {
		expect(rankFiles(paths, "ports/").map((item) => item.value)).toEqual([
			"backend/internal/ports/chat.go",
		]);
	});

	it("shows the basename as the label and the parent as the detail", () => {
		const [row] = rankFiles(["backend/internal/ports/chat.go"], "chat.go");
		expect(row).toMatchObject({
			value: "backend/internal/ports/chat.go",
			label: "chat.go",
			detail: "backend/internal/ports",
		});
	});

	// A file at the repo root has no parent directory. Deriving one from a negative
	// index truncated its own name and showed "AGENTS.m" under "AGENTS.md".
	it("gives a root-level file no parent detail", () => {
		const [row] = rankFiles(["AGENTS.md"], "agents");
		expect(row).toMatchObject({ value: "AGENTS.md", label: "AGENTS.md" });
		expect(row?.detail).toBeUndefined();
	});

	it("prefers a shallower path among equal matches", () => {
		const ranked = rankFiles(["a/b/c/notes.md", "notes.md"], "notes");
		expect(ranked[0]?.value).toBe("notes.md");
	});
});

describe("moveHighlight", () => {
	it("wraps at both ends so a short list is fully reachable by keyboard", () => {
		expect(moveHighlight(0, -1, 3)).toBe(2);
		expect(moveHighlight(2, 1, 3)).toBe(0);
		expect(moveHighlight(0, 1, 3)).toBe(1);
	});

	it("stays at zero with nothing to highlight", () => {
		expect(moveHighlight(0, 1, 0)).toBe(0);
	});
});
