import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpSuggestion } from "../../lib/cloud-cp/types";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import { AsideActionsContext, AsideRow, type AsideActions } from "./AsideRow";

function aside(overrides: Partial<NonNullable<NonNullable<ConversationActivity["detail"]>["aside"]>> = {}): ConversationActivity {
	return {
		kind: "activity", id: "cloud-aside-a1", sequence: 4, revision: 1, activityKind: "system", status: "completed",
		summary: "Looks right", createdAt: "2026-11-07T10:00:00Z",
		detail: { event: "chat.aside", aside: { asideId: "a1", text: "Looks right.\nOne nit: cover the expired cookie.", authorUserId: "u-chen", authorName: "Chen Wei", mentions: [], ...overrides } },
	};
}

function renderRow(activity: ConversationActivity, actions: AsideActions = {}) {
	return render(
		<TooltipProvider>
			<AsideActionsContext.Provider value={actions}>
				<AsideRow activity={activity} />
			</AsideActionsContext.Provider>
		</TooltipProvider>,
	);
}

describe("AsideRow", () => {
	it("reads as a note: author, text, and an eye-off mark that says the agent can't see it", () => {
		const { container } = renderRow(aside());
		expect(screen.getByText("Chen Wei")).toBeInTheDocument();
		expect(screen.getByText(/One nit: cover the expired cookie/)).toBeInTheDocument();
		// The words are the mark's name and tooltip, not repeated text on every row.
		expect(screen.getByRole("img", { name: "Agent can't see this" })).toBeInTheDocument();
		expect(screen.queryByText("Agent can't see this")).toBeNull();
		const edge = container.querySelector("[data-aside]")!.className;
		expect(edge).toContain("border-dashed");
		// The muted text color in full: 3:1 or better in both themes, never a faint tint of it.
		expect(edge).toMatch(/border-muted-foreground(\s|$)/);
		expect(screen.getByRole("img", { name: "Agent can't see this" }).className).toContain("text-muted-foreground");
		// Nothing to do without a way to suggest it.
		expect(screen.queryByRole("button", { name: /Suggest to agent/ })).toBeNull();
	});

	it("says You for the person's own aside", () => {
		renderRow(aside(), { selfUserId: "u-chen" });
		expect(screen.getByText("You")).toBeInTheDocument();
	});

	it("names the author the event names, whoever else the session knows", () => {
		renderRow(aside({ authorName: "Chen Wei" }), { nameOf: () => "Somebody Else" });
		expect(screen.getByText("Chen Wei")).toBeInTheDocument();
		expect(screen.queryByText("Somebody Else")).toBeNull();
	});

	it("names the author of an aside from before names were recorded from the session", () => {
		renderRow(aside({ authorName: "" }), { nameOf: (userId) => (userId === "u-chen" ? "Chen Wei" : undefined) });
		expect(screen.getByText("Chen Wei")).toBeInTheDocument();
	});

	it("falls back to Someone when nobody knows that author", () => {
		renderRow(aside({ authorName: "" }));
		expect(screen.getByText("Someone")).toBeInTheDocument();
	});

	it("puts the aside forward with Suggest to agent", async () => {
		const onSuggest = vi.fn(async () => undefined);
		renderRow(aside(), { onSuggest });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Suggest to agent" })));
		expect(onSuggest).toHaveBeenCalledWith("a1");
	});

	it("says so when suggesting failed, so it can be tried again", async () => {
		const onSuggest = vi.fn(async () => { throw new Error("nope"); });
		renderRow(aside(), { onSuggest });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Suggest to agent" })));
		expect(screen.getByRole("button", { name: /try again/ })).toBeInTheDocument();
	});

	const waiting = (suggestedBy: string): CloudCpSuggestion => ({
		id: "sg-1", orgId: "o", sessionId: "s", kind: "aside", asideId: "a1", text: "Looks right", status: "pending",
		suggestedBy: { userId: suggestedBy, name: "Chen Wei" }, author: { userId: "u-chen", name: "Chen Wei" },
		createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z",
	});

	it("shows an aside already waiting for the driver as Suggested", () => {
		renderRow(aside(), { onSuggest: vi.fn(), onWithdraw: vi.fn(), selfUserId: "u-other", suggestionFor: () => waiting("u-chen") });
		expect(screen.getByText("Suggested")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Suggest to agent" })).toBeNull();
		// Someone else's suggestion is the driver's to decline, not theirs.
		expect(screen.queryByRole("button", { name: "Withdraw" })).toBeNull();
	});

	it("lets the person who suggested it withdraw it", async () => {
		const suggestion = waiting("u-me");
		const onWithdraw = vi.fn(async () => undefined);
		renderRow(aside(), { onSuggest: vi.fn(), onWithdraw, selfUserId: "u-me", suggestionFor: () => suggestion });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Withdraw" })));
		expect(onWithdraw).toHaveBeenCalledWith(suggestion);
	});

	it("says quietly that it was sent to the agent, instead of offering Suggest again", () => {
		renderRow(aside(), { onSuggest: vi.fn(), selfUserId: "u-me", suggestionFor: () => undefined, sentFor: (asideId) => asideId === "a1" });
		expect(screen.getByText("Sent to agent")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Suggest to agent" })).toBeNull();
	});

	it("may be suggested again once its suggestion was dismissed", () => {
		renderRow(aside(), { onSuggest: vi.fn(), selfUserId: "u-me", suggestionFor: () => undefined, sentFor: () => false });
		expect(screen.queryByText("Sent to agent")).toBeNull();
		expect(screen.getByRole("button", { name: "Suggest to agent" })).toBeInTheDocument();
	});

	it("draws the people and teams it names as chips", () => {
		const { container } = renderRow(aside({ text: "@Chen Wei and @Platform, can you check?", mentions: [{ kind: "person", userId: "u-chen", name: "Chen Wei" }, { kind: "team", teamId: "t", name: "Platform" }] }));
		const chips = [...container.querySelectorAll("[data-mention-chip]")];
		expect(chips.map((chip) => [chip.getAttribute("data-mention-chip"), chip.textContent])).toEqual([["person", "CWChen Wei"], ["team", "PLPlatform"]]);
		expect(container.querySelector("[data-aside] p")?.textContent).toBe("CWChen Wei and PLPlatform, can you check?");
	});

	it("gives your own aside your initials, not the label's", () => {
		const { container } = renderRow(aside({ authorUserId: "u-me", authorName: "Asha Rao" }), { selfUserId: "u-me" });
		expect(screen.getByText("You")).toBeInTheDocument();
		expect(container.querySelector("[data-person-avatar]")?.textContent).toBe("AR");
	});

	it("renders nothing for a row without an aside", () => {
		const { container } = renderRow({ ...aside(), detail: { event: "chat.aside" } });
		expect(container.querySelector("[data-aside]")).toBeNull();
	});
});
