import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MentionAccessBar, MentionTeamBar, SuggestionBar, joinNames } from "./TeamComposerBars";

const suggestion = {
	suggestionId: "sg-1", asideId: "a1", text: "Also cover a refresh after the cookie expires.",
	suggestedBy: { userId: "u-chen", name: "Chen Wei" }, createdAt: "2026-11-07T10:00:00Z",
};

describe("SuggestionBar", () => {
	it("says who suggests what on one line, with Send, Edit and Dismiss", () => {
		const onSend = vi.fn();
		const onEdit = vi.fn();
		const onDismiss = vi.fn();
		render(<SuggestionBar suggestion={suggestion} onSend={onSend} onEdit={onEdit} onDismiss={onDismiss} />);
		expect(screen.getByRole("region", { name: "Chen Wei suggests" })).toHaveTextContent("Chen Wei suggests Also cover a refresh after the cookie expires.");
		fireEvent.click(screen.getByRole("button", { name: "Send" }));
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
		expect(onSend).toHaveBeenCalledTimes(1);
		expect(onEdit).toHaveBeenCalledTimes(1);
		expect(onDismiss).toHaveBeenCalledTimes(1);
	});

	it("holds its buttons while one runs, and says when one failed", () => {
		render(<SuggestionBar suggestion={suggestion} onSend={vi.fn()} onEdit={vi.fn()} onDismiss={vi.fn()} busy failed />);
		expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't do that. Try again.");
	});
});

describe("MentionAccessBar", () => {
	const people = [
		{ kind: "person" as const, id: "u-cara", name: "Cara Diaz", canOpen: false },
		{ kind: "person" as const, id: "u-farah", name: "Farah Nasser", canOpen: false },
	];

	it("names who can't open the session, and offers Share and send or Send without them", () => {
		const onShare = vi.fn();
		const onSkip = vi.fn();
		render(<MentionAccessBar people={people} onShare={onShare} onSkip={onSkip} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Cara Diaz and Farah Nasser can't open this session");
		fireEvent.click(screen.getByRole("button", { name: "Share and send" }));
		fireEvent.click(screen.getByRole("button", { name: "Send without them" }));
		expect(onShare).toHaveBeenCalledTimes(1);
		expect(onSkip).toHaveBeenCalledTimes(1);
	});

	it("holds the choice while sharing and says when sharing failed", () => {
		render(<MentionAccessBar people={people.slice(0, 1)} onShare={vi.fn()} onSkip={vi.fn()} sharing failed />);
		expect(screen.getByRole("button", { name: "Share and send" })).toBeDisabled();
		expect(screen.getByText("Couldn't share. Try again, or send without them.")).toBeInTheDocument();
	});
});

describe("MentionTeamBar", () => {
	it("asks before notifying a big team, and says how many can't open the session", () => {
		const onNotify = vi.fn();
		const onCancel = vi.fn();
		render(<MentionTeamBar team={{ kind: "team", id: "t", name: "Platform", memberCount: 6, membersWithoutAccess: 2 }} onNotify={onNotify} onCancel={onCancel} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Notify 6 people in Platform? 2 of them can't open this session");
		fireEvent.click(screen.getByRole("button", { name: "Notify" }));
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(onNotify).toHaveBeenCalledTimes(1);
		expect(onCancel).toHaveBeenCalledTimes(1);
	});

	it("leaves the access line out when everyone can open it", () => {
		render(<MentionTeamBar team={{ kind: "team", id: "t", name: "Platform", memberCount: 7, membersWithoutAccess: 0 }} onNotify={vi.fn()} onCancel={vi.fn()} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Notify 7 people in Platform?");
		expect(screen.getByRole("alert")).not.toHaveTextContent("can't open");
	});
});

describe("joinNames", () => {
	it("joins names the way the language does", () => {
		expect(joinNames(["Ben"], "en")).toBe("Ben");
		expect(joinNames(["Ben", "Dana", "Omar"], "en")).toBe("Ben, Dana, and Omar");
	});
});
