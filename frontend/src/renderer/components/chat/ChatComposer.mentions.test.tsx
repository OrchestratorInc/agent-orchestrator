import { act, fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ComposerMention } from "../../lib/cloud-team-collab";
import { chatDraftScopeKey, readChatSessionDraft } from "../../lib/chat-drafts";
import { readDraftMentions } from "../../lib/chat-draft-mentions";
import type { CloudCpMentionCandidates } from "../../lib/cloud-cp/types";
import { useUiStore } from "../../stores/ui-store";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer, type ComposerMentionSource } from "./ChatComposer";

function render(ui: ReactElement) {
	return rtlRender(ui, { wrapper: TooltipProvider });
}

const FILES = ["src/auth/session.ts", "src/auth/token.ts", "src/checkout/cart.ts", "README.md"];

const CANDIDATES: CloudCpMentionCandidates = {
	people: [
		{ userId: "u-chen", name: "Chen Wei", inSession: true, canOpen: true },
		{ userId: "u-cara", name: "Cara Diaz", inSession: false, canOpen: false },
		{ userId: "u-ben", name: "Ben Ortiz", inSession: true, canOpen: true },
		{ userId: "u-me", name: "Asha Rao", inSession: true, canOpen: true },
	],
	teams: [
		{ teamId: "t-checkout", name: "Checkout", memberCount: 3, membersWithoutAccess: 0, namesWithoutAccess: [] },
		{ teamId: "t-platform", name: "Platform", memberCount: 6, membersWithoutAccess: 2, namesWithoutAccess: ["Dana Kim", "Eli Park"] },
	],
};

function mentions(overrides: Partial<ComposerMentionSource> = {}): ComposerMentionSource {
	return {
		onQuery: vi.fn(),
		candidates: CANDIDATES,
		workspaceName: "Northwind",
		presentUserIds: new Set(["u-chen"]),
		selfUserId: "u-me",
		...overrides,
	};
}

function renderComposer(props: Partial<Parameters<typeof ChatComposer>[0]> = {}) {
	const onSend = vi.fn();
	const view = render(<ChatComposer onSend={onSend} filePaths={FILES} {...props} />);
	return { onSend, view, field: screen.getByLabelText((props.editorLabel as string | undefined) ?? "Message the agent") as HTMLElement };
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

describe("the @ menu with people and teams", () => {
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
		// The signed-in person is never offered; whoever is here now says so.
		expect(outline.join("\n")).not.toContain("Asha Rao");
		expect(outline[1]).toContain("Chen Wei");
		expect(outline[1]).toContain("Here now");
		expect(outline.find((line) => line.includes("Platform"))).toContain("6 people");
		expect(outline.find((line) => line.includes("Cara Diaz"))).toContain("Can't open this yet");
	});

	it("shows at most five people under In this session", async () => {
		const people = Array.from({ length: 8 }, (_, index) => ({ userId: `u-${index}`, name: `Person ${index}`, inSession: true, canOpen: true }));
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
		const rows = outline.filter((line) => !line.startsWith("# "));
		expect(rows).toHaveLength(2);
		expect(rows).toEqual(expect.arrayContaining([expect.stringContaining("session.ts"), expect.stringContaining("token.ts")]));
		await waitFor(() => expect(source.onQuery).toHaveBeenLastCalledWith(undefined));
	});

	it("never opens inside an email address", async () => {
		const source = mentions();
		const { field } = renderComposer({ mentions: source });
		await typeInLexicalEditor(field, "write to ana@north");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(source.onQuery).not.toHaveBeenCalledWith(expect.any(String));
	});

	it("never offers a person for a package name like @scope/pkg", async () => {
		const { field } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "install @types/node");
		// No file matches either, so the menu stays shut.
		expect(screen.queryByRole("listbox")).toBeNull();
	});

	it("keeps files only, as today, without a mention source", async () => {
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
		const chip = field.querySelector('[data-composer-token="person"]');
		expect(chip).toHaveTextContent("Chen Wei");
		await typeInLexicalEditor(field, "can you look?");
		expect(lexicalEditorText(field)).toBe("@Chen Wei can you look?");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		const call = onSend.mock.calls[0]!;
		expect(call[0]).toBe("@Chen Wei can you look?");
		expect(call[7]).toEqual([{ kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true }]);
	});

	it("inserts a team chip by click, with what the menu knew about its size", async () => {
		const { field, onSend } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "@");
		fireEvent.click(within(screen.getByRole("listbox")).getByText("Platform"));
		expect(field.querySelector('[data-composer-token="team"]')).toHaveTextContent("Platform");
		await typeInLexicalEditor(field, "heads up");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		expect(onSend.mock.calls[0]![7]).toEqual([{ kind: "team", id: "t-platform", name: "Platform", memberCount: 6, membersWithoutAccess: 2, namesWithoutAccess: ["Dana Kim", "Eli Park"] }]);
	});

	it("keeps a file pick as today's file chip", async () => {
		const { field, onSend } = renderComposer({ mentions: mentions() });
		await typeInLexicalEditor(field, "@cart");
		await userEvent.keyboard("{Enter}{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith("src/checkout/cart.ts"));
	});

	it("brings a saved draft's chips back when the composer mounts again", async () => {
		const first = renderComposer({ mentions: mentions(), draftSessionId: "session-1" });
		await typeInLexicalEditor(first.field, "@che");
		await userEvent.keyboard("{Enter}");
		await typeInLexicalEditor(first.field, "hi");
		expect(readDraftMentions(chatDraftScopeKey({ sessionId: "session-1", incarnation: "session-1" }))).toEqual([
			{ kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true },
		]);
		first.view.unmount();
		const second = renderComposer({ mentions: mentions(), draftSessionId: "session-1" });
		await waitFor(() => expect(second.field.querySelector('[data-composer-token="person"]')).toHaveTextContent("Chen Wei"));
		expect(lexicalEditorText(second.field)).toBe("@Chen Wei hi");
	});
});

describe("asking before a mentioning send", () => {
	const chen: ComposerMention = { kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true };

	it("waits for the answer, then sends the mentions it cleared", async () => {
		let answer!: (mentions: ComposerMention[] | null) => void;
		const confirm = vi.fn(() => new Promise<ComposerMention[] | null>((resolve) => { answer = resolve; }));
		const { field, onSend } = renderComposer({ mentions: mentions({ confirm }) });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}");
		await userEvent.keyboard("{Enter}");
		expect(confirm).toHaveBeenCalledWith([chen]);
		expect(onSend).not.toHaveBeenCalled();
		// The draft stays where it was while the question is open.
		expect(lexicalEditorText(field)).toBe("@Chen Wei ");
		await act(async () => answer([]));
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		expect(onSend.mock.calls[0]![0]).toBe("@Chen Wei");
		// Cleared to send with nobody: no tokens go.
		expect(onSend.mock.calls[0]![7]).toBeUndefined();
	});

	it("keeps the draft unsent when the answer is no", async () => {
		const confirm = vi.fn(() => Promise.resolve(null));
		const { field, onSend } = renderComposer({ mentions: mentions({ confirm }) });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}{Enter}");
		await act(async () => undefined);
		expect(onSend).not.toHaveBeenCalled();
		expect(lexicalEditorText(field)).toBe("@Chen Wei ");
	});

	it("drops the question when the draft changes, and asks again on the next send", async () => {
		const confirm = vi.fn(() => new Promise<ComposerMention[] | null>(() => undefined));
		const cancelConfirm = vi.fn();
		const { field } = renderComposer({ mentions: mentions({ confirm, cancelConfirm }) });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}{Enter}");
		expect(confirm).toHaveBeenCalledTimes(1);
		await typeInLexicalEditor(field, "x");
		expect(cancelConfirm).toHaveBeenCalled();
		await userEvent.keyboard("{Enter}");
		expect(confirm).toHaveBeenCalledTimes(2);
	});

	it("drops the question on Escape", async () => {
		const confirm = vi.fn(() => new Promise<ComposerMention[] | null>(() => undefined));
		const cancelConfirm = vi.fn();
		const { field } = renderComposer({ mentions: mentions({ confirm, cancelConfirm }) });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}{Enter}");
		fireEvent.keyDown(field, { key: "Escape" });
		expect(cancelConfirm).toHaveBeenCalled();
	});

	it("sends at once when nothing needs asking", async () => {
		const confirm = vi.fn(() => undefined);
		const { field, onSend } = renderComposer({ mentions: mentions({ confirm }) });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		expect(onSend.mock.calls[0]![7]).toEqual([chen]);
	});
});

describe("mentions in a steer", () => {
	it("asks first, then steers with the mentions it cleared", async () => {
		let answer!: (mentions: ComposerMention[] | null) => void;
		const confirm = vi.fn(() => new Promise<ComposerMention[] | null>((resolve) => { answer = resolve; }));
		const onSteer = vi.fn(async () => ({ status: "accepted" as const }));
		const { field, onSend } = renderComposer({ mentions: mentions({ confirm }), willQueue: true, canSteer: true, onSteer });
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}");
		await typeInLexicalEditor(field, "look");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");
		expect(confirm).toHaveBeenCalledTimes(1);
		expect(onSteer).not.toHaveBeenCalled();
		await act(async () => answer([{ kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true }]));
		await waitFor(() => expect(onSteer).toHaveBeenCalled());
		const call = onSteer.mock.calls[0] as unknown[];
		expect(call[0]).toBe("@Chen Wei look");
		expect(call[4]).toEqual([{ kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true }]);
		expect(onSend).not.toHaveBeenCalled();
	});
});

describe("composer modes", () => {
	it("switches mode on Shift+Tab instead of moving focus", async () => {
		const onToggleMode = vi.fn();
		const { field } = renderComposer({ onToggleMode });
		const event = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
		act(() => { field.dispatchEvent(event); });
		expect(onToggleMode).toHaveBeenCalledTimes(1);
		expect(event.defaultPrevented).toBe(true);
	});

	it("leaves Shift+Tab alone without a mode to switch", async () => {
		const { field } = renderComposer();
		const event = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
		act(() => { field.dispatchEvent(event); });
		expect(event.defaultPrevented).toBe(false);
	});

	it("names the editor for the mode", () => {
		renderComposer({ editorLabel: "Message teammates" });
		expect(screen.getByLabelText("Message teammates")).toBeInTheDocument();
	});
});

describe("mentions on a saved draft's delivery", () => {
	it("sends them with the delivery and keeps them on its record for a retry", async () => {
		let fail = true;
		const onSend = vi.fn(async () => {
			if (fail) throw Object.assign(new Error("offline"), { code: "NETWORK" });
		});
		render(<ChatComposer onSend={onSend} filePaths={FILES} mentions={mentions()} draftSessionId="session-2" parkUnsentSends={false} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;
		await typeInLexicalEditor(field, "@che");
		await userEvent.keyboard("{Enter}");
		await typeInLexicalEditor(field, "ping");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		const first = onSend.mock.calls[0] as unknown[];
		expect(first[0]).toBe("@Chen Wei ping");
		expect(first[7]).toEqual([{ kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true }]);
		const draft = readChatSessionDraft({ sessionId: "session-2", incarnation: "session-2" });
		expect(draft.composer.delivery?.mentions).toEqual(first[7]);
		// The retry goes with the same id and the same mentions.
		fail = false;
		await userEvent.click(await screen.findByRole("button", { name: /retry/i }));
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		const second = onSend.mock.calls[1] as unknown[];
		expect(second[2]).toBe(first[2]);
		expect(second[7]).toEqual(first[7]);
	});
});

describe("calm refusals", () => {
	it("reads an ordinary refusal calmly and puts the text back", async () => {
		const onSend = vi.fn(async () => {
			throw Object.assign(new Error("Already sent or dismissed."), { code: "suggestion_resolved", calm: true });
		});
		render(<ChatComposer onSend={onSend} draftSessionId="session-calm" />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;
		await typeInLexicalEditor(field, "Edited suggestion");
		await userEvent.keyboard("{Enter}");
		const line = await screen.findByText("Already sent or dismissed.");
		expect(line).toHaveAttribute("role", "status");
		expect(line).toHaveClass("text-muted-foreground");
		expect(screen.queryByRole("alert")).toBeNull();
		await waitFor(() => expect(lexicalEditorText(field)).toBe("Edited suggestion"));
	});

	it("still reads a real refusal as an error", async () => {
		const onSend = vi.fn(async () => {
			throw Object.assign(new Error("The request body is invalid."), { code: "INVALID_BODY" });
		});
		render(<ChatComposer onSend={onSend} draftSessionId="session-error" />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;
		await typeInLexicalEditor(field, "Hello");
		await userEvent.keyboard("{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("The request body is invalid.");
	});
});

describe("the mention cap", () => {
	it("holds a send that names more than 20 people and teams, saying so calmly", async () => {
		const name = (index: number) => `P${String(index).padStart(2, "0")}`;
		const people = Array.from({ length: 21 }, (_, index) => ({ userId: `u-${index}`, name: name(index), inSession: true, canOpen: true }));
		const onSend = vi.fn();
		render(<ChatComposer onSend={onSend} mentions={mentions({ candidates: { people, teams: [] } })} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;
		for (let index = 0; index < 21; index++) {
			await typeInLexicalEditor(field, `@${name(index)}`);
			await userEvent.keyboard("{Enter}");
		}
		expect(field.querySelectorAll('[data-composer-token="person"]')).toHaveLength(21);
		await userEvent.keyboard("{Enter}");
		const line = await screen.findByText("Mention at most 20 people and teams at once.");
		expect(line).toHaveAttribute("role", "status");
		expect(onSend).not.toHaveBeenCalled();
	});
});
