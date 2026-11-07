import { fireEvent, render as renderPlain, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpSuggestion } from "../../lib/cloud-cp/types";
import { TooltipProvider } from "../ui/tooltip";
import { MentionAccessBar, MentionTeamBar, SuggestionBar, SuggestionEditBar, SuggestionNotice, joinNames } from "./TeamComposerBars";

const render = (ui: ReactElement) => renderPlain(<TooltipProvider>{ui}</TooltipProvider>);

const suggestion: CloudCpSuggestion = {
	id: "sg-1", orgId: "org-1", sessionId: "s-1", kind: "aside", asideId: "a1", text: "Also cover a refresh after the cookie expires.",
	suggestedBy: { userId: "u-chen", name: "Chen Wei" }, author: { userId: "u-chen", name: "Chen Wei" }, status: "pending",
	createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z",
};

describe("SuggestionBar", () => {
	it("says who suggests what on one line, with Send and Edit, and a ✕ to dismiss", () => {
		const onSend = vi.fn();
		const onEdit = vi.fn();
		const onDismiss = vi.fn();
		render(<SuggestionBar suggestion={suggestion} onSend={onSend} onEdit={onEdit} onDismiss={onDismiss} />);
		expect(screen.getByRole("region", { name: "Chen Wei suggests:" })).toHaveTextContent("Chen Wei suggests: Also cover a refresh after the cookie expires.");
		fireEvent.click(screen.getByRole("button", { name: "Send" }));
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		const dismiss = screen.getByRole("button", { name: "Dismiss" });
		// Dismiss is an icon, named for screen readers and in its tooltip.
		expect(dismiss).toHaveTextContent("");
		fireEvent.click(dismiss);
		expect(onSend).toHaveBeenCalledTimes(1);
		expect(onEdit).toHaveBeenCalledTimes(1);
		expect(onDismiss).toHaveBeenCalledTimes(1);
	});

	it("holds every button while one runs, spins the one running, and says when one failed", () => {
		const { container, rerender } = render(<SuggestionBar suggestion={suggestion} onSend={vi.fn()} onEdit={vi.fn()} onDismiss={vi.fn()} pending="send" notice={{ text: "Couldn't do that. Try again.", tone: "error" }} />);
		expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Edit" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Dismiss" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Send" }).querySelector(".animate-spin")).not.toBeNull();
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't do that. Try again.");
		rerender(<TooltipProvider><SuggestionBar suggestion={suggestion} onSend={vi.fn()} onEdit={vi.fn()} onDismiss={vi.fn()} pending="dismiss" /></TooltipProvider>);
		expect(screen.getByRole("button", { name: "Dismiss" }).querySelector(".animate-spin")).not.toBeNull();
		expect(container.querySelectorAll(".animate-spin")).toHaveLength(1);
	});

	it("says an ordinary outcome on one calm line", () => {
		render(<SuggestionBar suggestion={suggestion} onSend={vi.fn()} onEdit={vi.fn()} onDismiss={vi.fn()} notice={{ text: "Someone else is driving now.", tone: "calm" }} />);
		const line = screen.getByRole("status");
		expect(line).toHaveTextContent("Someone else is driving now.");
		expect(line).toHaveClass("text-muted-foreground");
		expect(screen.queryByRole("alert")).toBeNull();
	});
});

describe("SuggestionEditBar and SuggestionNotice", () => {
	it("says whose suggestion is being edited, with a way back", () => {
		const onCancel = vi.fn();
		render(<SuggestionEditBar suggestion={suggestion} onCancel={onCancel} />);
		expect(screen.getByRole("region", { name: "Editing Chen Wei's suggestion" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(onCancel).toHaveBeenCalledTimes(1);
	});

	it("carries a calm line on its own", () => {
		render(<SuggestionNotice text="Already sent or dismissed." tone="calm" />);
		expect(screen.getByRole("status")).toHaveTextContent("Already sent or dismissed.");
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
		render(<MentionAccessBar people={people.slice(0, 1)} onShare={vi.fn()} onSkip={vi.fn()} sharing problem="failed" />);
		expect(screen.getByRole("button", { name: "Share and send" })).toBeDisabled();
		expect(screen.getByText("Couldn't share. Try again, or send without them.")).toHaveClass("text-error");
	});

	it("says calmly when the control plane doesn't know someone, keeping Send without them", () => {
		render(<MentionAccessBar people={people.slice(0, 1)} onShare={vi.fn()} onSkip={vi.fn()} problem="unknown-person" />);
		const line = screen.getByText("Couldn't share with Cara Diaz. Send without them?");
		expect(line).toHaveAttribute("role", "status");
		expect(line).toHaveClass("text-muted-foreground");
		expect(screen.getByRole("button", { name: "Send without them" })).toBeEnabled();
	});
});

describe("MentionTeamBar", () => {
	it("names who it misses when it knows all of them", () => {
		render(<MentionTeamBar team={{ kind: "team", id: "t", name: "Platform", memberCount: 6, membersWithoutAccess: 2, namesWithoutAccess: ["Dana Kim", "Eli Park"] }} onNotify={vi.fn()} onCancel={vi.fn()} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Notify 6 people in Platform? Dana Kim and Eli Park can't open this session");
	});

	it("asks before notifying a big team, and says how many can't open the session", () => {
		const onCancel = vi.fn();
		render(<MentionTeamBar team={{ kind: "team", id: "t", name: "Platform", memberCount: 6, membersWithoutAccess: 2, namesWithoutAccess: ["Dana Kim"] }} onNotify={vi.fn()} onCancel={onCancel} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Notify 6 people in Platform? 2 of them can't open this session");
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(onCancel).toHaveBeenCalledTimes(1);
	});

	it("notifies once: a second click finds Notify busy", () => {
		const onNotify = vi.fn();
		render(<MentionTeamBar team={{ kind: "team", id: "t", name: "Platform", memberCount: 6 }} onNotify={onNotify} onCancel={vi.fn()} />);
		const notify = screen.getByRole("button", { name: "Notify" });
		fireEvent.click(notify);
		fireEvent.click(notify);
		expect(onNotify).toHaveBeenCalledTimes(1);
		expect(notify).toBeDisabled();
		expect(notify.querySelector(".animate-spin")).not.toBeNull();
		expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
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
