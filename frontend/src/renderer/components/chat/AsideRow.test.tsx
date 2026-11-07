import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
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
	it("reads as a note: author, text, and that the agent can't see it", () => {
		const { container } = renderRow(aside());
		expect(screen.getByText("Chen Wei")).toBeInTheDocument();
		expect(screen.getByText(/One nit: cover the expired cookie/)).toBeInTheDocument();
		expect(screen.getByText("Agent can't see this")).toBeInTheDocument();
		expect(container.querySelector("[data-aside]")?.className).toContain("border-dashed");
		// Nothing to do without a way to suggest it.
		expect(screen.queryByRole("button", { name: /Suggest to agent/ })).toBeNull();
	});

	it("says You for the person's own aside", () => {
		renderRow(aside(), { selfUserId: "u-chen" });
		expect(screen.getByText("You")).toBeInTheDocument();
	});

	it("names an author the event doesn't name from the people in the session, else Someone", () => {
		renderRow(aside({ authorName: undefined }), { nameOf: (userId) => (userId === "u-chen" ? "Chen Wei" : undefined) });
		expect(screen.getByText("Chen Wei")).toBeInTheDocument();
	});

	it("falls back to Someone when nobody knows the author", () => {
		renderRow(aside({ authorName: undefined }));
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

	it("shows an aside already waiting for the driver as Suggested", () => {
		renderRow(aside(), { onSuggest: vi.fn(), suggestedAsideIds: new Set(["a1"]) });
		expect(screen.getByText("Suggested")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Suggest to agent" })).toBeNull();
	});

	it("renders nothing for a row without an aside", () => {
		const { container } = renderRow({ ...aside(), detail: { event: "chat.aside" } });
		expect(container.querySelector("[data-aside]")).toBeNull();
	});
});
