import { act, fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatDraftScopeKey } from "../../lib/chat-drafts";
import { readDraftMentions, writeDraftMentions } from "../../lib/chat-draft-mentions";
import type { CloudCpMentionCandidates } from "../../lib/cloud-cp/types";
import { useUiStore } from "../../stores/ui-store";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer, type ComposerMentionSource } from "./ChatComposer";

function render(ui: ReactElement) {
	return rtlRender(ui, { wrapper: TooltipProvider });
}

const FILES = ["src/auth/session.ts", "src/auth/token.ts", "src/checkout/cart.ts", "README.md"];

// What the control plane answers for a member: people who can open the
// session (never the caller, though the menu also drops them), and teams.
const CANDIDATES: CloudCpMentionCandidates = {
	people: [
		{ userId: "u-ben", name: "Ben Ortiz", present: false, inSession: true },
		{ userId: "u-chen", name: "Chen Wei", present: true, inSession: true },
		{ userId: "u-cara", name: "Cara Diaz", present: false, inSession: false },
		{ userId: "u-me", name: "Asha Rao", present: true, inSession: true },
	],
	teams: [
		{ teamId: "t-checkout", name: "Checkout" },
		{ teamId: "t-platform", name: "Platform" },
	],
};

function mentions(overrides: Partial<ComposerMentionSource> = {}): ComposerMentionSource {
	return {
		onQuery: vi.fn(),
		candidates: CANDIDATES,
		workspaceName: "Northwind",
		selfUserId: "u-me",
		...overrides,
	};
}

function renderComposer(props: Partial<Parameters<typeof ChatComposer>[0]> = {}) {
	const onSend = vi.fn();
	const view = render(<ChatComposer onSend={onSend} filePaths={FILES} {...props} />);
	return { onSend, view, field: screen.getByLabelText("Message the agent") as HTMLElement };
}

/** Section headings and row labels, top to bottom, as the menu reads. */
function menuOutline(): string[] {
	const listbox = screen.getByRole("listbox");
	return [...listbox.querySelectorAll("[data-suggest-section], [role=option]")].map((node) =>
		node.hasAttribute("data-suggest-section") ? `# ${node.textContent}` : (node.textContent ?? "").replace(/\s+/g, " ").trim());
}

beforeEach(() => {
	localStorage.clear();
	useUiStore.setState({ chatSendKeyMode: "enter" });
});

describe("the @ menu", () => {
	it("lists the session's people, teams, the workspace's people, then files, in that order", async () => {
		const { field } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "check with @");
		const outline = menuOutline();
		expect(outline.filter((line) => line.startsWith("# "))).toEqual([
			"# In this session",
			"# Teams",
			"# People in Northwind",
			"# Files",
		]);
		// Those here now come first and say so; the signed-in person is never offered.
		expect(outline[1]).toContain("Chen Wei");
		expect(outline[1]).toContain("Here now");
		expect(outline[2]).toContain("Ben Ortiz");
		expect(outline[2]).not.toContain("Here now");
		expect(outline.join("\n")).not.toContain("Asha Rao");
		// No team sizes and no "can't open" labels: everyone listed can open the session.
		expect(outline.filter((line) => !line.startsWith("# ")).join("\n")).not.toMatch(/\d+ (people|person)|can't open/i);
	});

	it("names each section for screen readers as a group of its options", async () => {
		const { field } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "check with @");
		const listbox = screen.getByRole("listbox");
		const groups = within(listbox).getAllByRole("group");
		expect(groups.map((group) => group.getAttribute("aria-label"))).toEqual(["In this session", "Teams", "People in Northwind", "Files"]);
		expect(within(within(listbox).getByRole("group", { name: "Teams" })).getAllByRole("option").map((option) => option.textContent)).toEqual([
			expect.stringContaining("Checkout"),
			expect.stringContaining("Platform"),
		]);
	});

	it("gives a guest only the people in the session, as the control plane answers for one", async () => {
		const guest = { people: [{ userId: "u-chen", name: "Chen Wei", present: true, inSession: true }], teams: [] };
		const { field } = renderComposer({ mentions: mentions({ candidates: guest, workspaceName: undefined }) });
		await typeInLexicalEditor(field, "@");
		const outline = menuOutline();
		expect(outline.filter((line) => line.startsWith("# "))).toEqual(["# In this session", "# Files"]);
		expect(outline.filter((line) => !line.startsWith("# ") && !line.includes("."))).toEqual([expect.stringContaining("Chen Wei")]);
	});

	it("offers only the people and teams the control plane returned", async () => {
		const { field } = renderComposer({ mentions: mentions({ candidates: { people: [], teams: [] } }) });
		await typeInLexicalEditor(field, "@");
		expect(menuOutline().filter((line) => line.startsWith("# "))).toEqual(["# Files"]);
	});

	it("shows at most five people under In this session", async () => {
		const people = Array.from({ length: 8 }, (_, index) => ({ userId: `u-${index}`, name: `Person ${index}`, present: false, inSession: true }));
		const { field } = renderComposer({ mentions: mentions({ candidates: { people, teams: [] } }) });
		await typeInLexicalEditor(field, "@");
		const outline = menuOutline();
		const start = outline.indexOf("# In this session");
		const next = outline.findIndex((line, index) => index > start && line.startsWith("# "));
		expect(next - start - 1).toBe(5);
	});

	it("names the workspace generically when its name is unknown", async () => {
		const { field } = renderComposer({ mentions: mentions({ workspaceName: undefined }) });
		await typeInLexicalEditor(field, "@");
		expect(menuOutline()).toContain("# People in this workspace");
	});

	it("narrows people and teams by what follows @, and reports the query", async () => {
		const source = mentions();
		const { field } = renderComposer({ mentions: source });
		await typeInLexicalEditor(field, "@pla");
		const outline = menuOutline();
		expect(outline.some((line) => line.includes("Platform"))).toBe(true);
		expect(outline.some((line) => line.includes("Chen Wei"))).toBe(false);
		await waitFor(() => expect(source.onQuery).toHaveBeenLastCalledWith("pla"));
	});

	it("shows only files for a query with a slash or a dot, and stops asking for people", async () => {
		const source = mentions();
		const { field } = renderComposer({ mentions: source });
		await typeInLexicalEditor(field, "@src/auth/");
		const outline = menuOutline();
		expect(outline.some((line) => line.startsWith("# In this session") || line.startsWith("# Teams"))).toBe(false);
		await waitFor(() => expect(source.onQuery).toHaveBeenLastCalledWith(undefined));
	});

	it("never opens inside an email address", async () => {
		const source = mentions();
		const { field } = renderComposer({ mentions: source });
		await typeInLexicalEditor(field, "write to ana@north");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(source.onQuery).not.toHaveBeenCalledWith(expect.any(String));
	});

	it("keeps files only, as before, without a mention source", async () => {
		const { field } = renderComposer();
		await typeInLexicalEditor(field, "@");
		expect(menuOutline().some((line) => line.startsWith("# "))).toBe(false);
	});
});

describe("mention chips", () => {
	it("inserts a person chip, sends its name as text and the person as a typed token", async () => {
		const { field, onSend } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}");
		expect(field.querySelector('[data-composer-token="person"]')).toHaveTextContent("Chen Wei");
		await typeInLexicalEditor(field, "can you look?");
		expect(lexicalEditorText(field)).toBe("@Chen Wei can you look?");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		const call = onSend.mock.calls[0]!;
		// The agent reads the text; the person travels beside it.
		expect(call[0]).toBe("@Chen Wei can you look?");
		expect(call[7]).toEqual([{ kind: "person", id: "u-chen", name: "Chen Wei" }]);
	});

	it("inserts a team chip by click; a team is mentioned like a person, with nothing asked", async () => {
		const { field, onSend } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "@");
		fireEvent.click(within(screen.getByRole("listbox")).getByText("Platform"));
		expect(field.querySelector('[data-composer-token="team"]')).toHaveTextContent("Platform");
		await typeInLexicalEditor(field, "heads up");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		expect(onSend.mock.calls[0]![0]).toBe("@Platform heads up");
		expect(onSend.mock.calls[0]![7]).toEqual([{ kind: "team", id: "t-platform", name: "Platform" }]);
	});

	it("sends a message without chips as before, with no tokens", async () => {
		const { field, onSend } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "no one named");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith("no one named"));
	});

	it("keeps a file pick as a file chip", async () => {
		const { field, onSend } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "@cart");
		await userEvent.keyboard("{Enter}{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith("src/checkout/cart.ts"));
	});

	it("carries the chips on a steer", async () => {
		const onSteer = vi.fn().mockResolvedValue(undefined);
		const { field, onSend } = renderComposer({ mentions: mentions(), onSteer, canSteer: true, willQueue: true });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}");
		await typeInLexicalEditor(field, "try the fake clock");
		act(() => {
			window.dispatchEvent(new KeyboardEvent("keydown", { key: "Meta", metaKey: true }));
		});
		await userEvent.click(screen.getByRole("button", { name: "Steer message" }));
		await waitFor(() => expect(onSteer).toHaveBeenCalled());
		const call = onSteer.mock.calls[0]!;
		expect(call[0]).toBe("@Chen Wei try the fake clock");
		expect(call[4]).toEqual([{ kind: "person", id: "u-chen", name: "Chen Wei" }]);
		expect(onSend).not.toHaveBeenCalled();
	});

	it("holds a send that names more than 20 people and teams", async () => {
		const people = Array.from({ length: 21 }, (_, index) => ({ userId: `u-${index}`, name: `Person${index}`, present: false, inSession: false }));
		const scope = chatDraftScopeKey({ sessionId: "session-many", incarnation: "session-many" });
		writeDraftMentions(scope, people.map((person) => ({ kind: "person" as const, id: person.userId, name: person.name })));
		const { field, onSend } = renderComposer({
			mentions: mentions({ candidates: { people, teams: [] } }),
			draftSeed: { id: "seed", text: people.map((person) => `@${person.name}`).join(" "), mentions: people.map((person) => ({ kind: "person" as const, id: person.userId, name: person.name })) },
		});
		await waitFor(() => expect(field.querySelectorAll('[data-composer-token="person"]')).toHaveLength(21));
		act(() => field.focus());
		await userEvent.keyboard("{Enter}");
		await screen.findByText("Mention at most 20 people and teams at once.");
		expect(onSend).not.toHaveBeenCalled();
	});

	it("brings a saved draft's chips back when the composer mounts again", async () => {
		const first = renderComposer({ mentions: mentions(), draftSessionId: "session-1" });
		await typeInLexicalEditor(first.field, "@che");
		await userEvent.keyboard("{Enter}");
		await typeInLexicalEditor(first.field, "hi");
		expect(readDraftMentions(chatDraftScopeKey({ sessionId: "session-1", incarnation: "session-1" }))).toEqual([
			{ kind: "person", id: "u-chen", name: "Chen Wei" },
		]);
		first.view.unmount();
		const second = renderComposer({ mentions: mentions(), draftSessionId: "session-1" });
		await waitFor(() => expect(second.field.querySelector('[data-composer-token="person"]')).toHaveTextContent("Chen Wei"));
		expect(lexicalEditorText(second.field)).toBe("@Chen Wei hi");
	});

	it("clears the saved chips once the draft is sent, so a later draft never revives them", async () => {
		const scope = chatDraftScopeKey({ sessionId: "session-chips", incarnation: "session-chips" });
		const first = renderComposer({ mentions: mentions(), draftSessionId: "session-chips" });
		await typeInLexicalEditor(first.field, "@che");
		await userEvent.keyboard("{Enter}");
		await typeInLexicalEditor(first.field, "hi");
		expect(readDraftMentions(scope)).toHaveLength(1);
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(first.onSend).toHaveBeenCalled());
		await waitFor(() => expect(localStorage.getItem(`ao.chatDraftMentions:${scope}`)).toBeNull());
	});
});
