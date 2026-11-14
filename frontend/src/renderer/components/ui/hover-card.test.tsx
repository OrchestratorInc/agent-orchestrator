import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Activity, type ReactNode, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HoverCard, HoverCardContent, HoverCardTrigger, dismissHoverCards } from "./hover-card";

// jsdom has no layout. Stand in for Chromium's checkVisibility: no box inside
// display: none, which is how a hidden <Activity> hides a kept-alive chat.
function checkVisibility(this: HTMLElement) {
	for (let element: HTMLElement | null = this; element; element = element.parentElement) {
		if (element.style.display === "none") return false;
	}
	return true;
}

function Card({
	mode = "visible",
	onOpenChange,
	triggerHidden = false,
	label = "Worker 180",
	children = "Session card",
}: {
	mode?: "visible" | "hidden";
	onOpenChange?: (open: boolean) => void;
	triggerHidden?: boolean;
	label?: string;
	children?: ReactNode;
}) {
	return (
		<Activity mode={mode}>
			<HoverCard defaultOpen onOpenChange={onOpenChange}>
				<HoverCardTrigger asChild>
					<button type="button" style={triggerHidden ? { display: "none" } : undefined}>{label}</button>
				</HoverCardTrigger>
				<HoverCardContent>{children}</HoverCardContent>
			</HoverCard>
		</Activity>
	);
}

function cardContent(text = "Session card") {
	return screen.getByText(text).closest<HTMLElement>("[data-slot=hover-card-content]")!;
}

describe("HoverCard dismissal", () => {
	beforeEach(() => {
		Object.defineProperty(HTMLElement.prototype, "checkVisibility", { configurable: true, value: checkVisibility });
	});

	afterEach(() => {
		delete (HTMLElement.prototype as Partial<HTMLElement>).checkVisibility;
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it("closes when its trigger is clicked", async () => {
		const onOpenChange = vi.fn();
		render(<Card onOpenChange={onOpenChange} />);
		expect(screen.getByText("Session card")).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Worker 180" }));

		await waitFor(() => expect(screen.queryByText("Session card")).not.toBeInTheDocument());
		expect(onOpenChange).toHaveBeenLastCalledWith(false);
	});

	it("stays closed after a click until the pointer comes back", () => {
		vi.useFakeTimers();
		render(
			<HoverCard openDelay={300}>
				<HoverCardTrigger asChild><button type="button">Worker 180</button></HoverCardTrigger>
				<HoverCardContent>Session card</HoverCardContent>
			</HoverCard>,
		);
		const trigger = screen.getByRole("button", { name: "Worker 180" });

		// The focus a click gives the trigger starts Radix's open timer.
		fireEvent.focus(trigger);
		fireEvent.click(trigger);
		act(() => vi.advanceTimersByTime(400));
		expect(screen.queryByText("Session card")).not.toBeInTheDocument();

		fireEvent.pointerLeave(trigger);
		fireEvent.pointerEnter(trigger);
		act(() => vi.advanceTimersByTime(400));
		expect(screen.getByText("Session card")).toBeInTheDocument();
	});

	it("closes when a link on the card is followed", async () => {
		render(<Card><a href="#pr-8611">PR #8611</a></Card>);

		fireEvent.click(screen.getByRole("link", { name: "PR #8611" }));

		await waitFor(() => expect(screen.queryByRole("link", { name: "PR #8611" })).not.toBeInTheDocument());
	});

	it("hides and closes when its trigger's view is kept alive hidden", async () => {
		const onOpenChange = vi.fn();
		const view = render(<Card onOpenChange={onOpenChange} />);
		const content = cardContent();

		// Opening the session hides the chat holding the link, but not the portaled card.
		view.rerender(<Card mode="hidden" onOpenChange={onOpenChange} />);

		await waitFor(() => expect(onOpenChange).toHaveBeenLastCalledWith(false));
		expect(!content.isConnected || content.style.display === "none").toBe(true);

		// Coming back to that chat does not bring the card back.
		view.rerender(<Card onOpenChange={onOpenChange} />);
		await waitFor(() => expect(screen.queryByText("Session card")).not.toBeInTheDocument());
	});

	it("is never shown for a trigger that is not laid out", async () => {
		const onOpenChange = vi.fn();
		render(<Card triggerHidden onOpenChange={onOpenChange} />);

		await waitFor(() => expect(onOpenChange).toHaveBeenLastCalledWith(false));
		const content = screen.queryByText("Session card")?.closest<HTMLElement>("[data-slot=hover-card-content]");
		expect(!content || content.style.display === "none").toBe(true);
	});

	it("closes when the window loses focus", async () => {
		vi.spyOn(document, "hasFocus").mockReturnValue(false);
		render(<Card />);

		act(() => {
			window.dispatchEvent(new Event("blur"));
		});

		await waitFor(() => expect(screen.queryByText("Session card")).not.toBeInTheDocument());
	});

	it("stays open when focus only moves into a frame on the page", async () => {
		// An artifact preview's page focusing itself blurs the window, not the document.
		vi.spyOn(document, "hasFocus").mockReturnValue(true);
		render(<Card />);

		act(() => {
			window.dispatchEvent(new Event("blur"));
		});
		await act(() => new Promise((resolve) => setTimeout(resolve, 10)));

		expect(screen.getByText("Session card")).toBeInTheDocument();
	});

	it("closes every open card when the route changes", async () => {
		render(
			<>
				<Card label="Worker 180">Session card</Card>
				<Card label="PR link">PR card</Card>
			</>,
		);
		expect(screen.getByText("PR card")).toBeInTheDocument();

		act(() => dismissHoverCards());

		await waitFor(() => {
			expect(screen.queryByText("Session card")).not.toBeInTheDocument();
			expect(screen.queryByText("PR card")).not.toBeInTheDocument();
		});
	});
});

function Timeline({ defaultOpen = true, onOpenChange }: { defaultOpen?: boolean; onOpenChange?: (open: boolean) => void }) {
	return (
		<div data-testid="timeline" style={{ overflow: "auto", height: 100 }}>
			<HoverCard defaultOpen={defaultOpen} onOpenChange={onOpenChange}>
				<HoverCardTrigger>trigger</HoverCardTrigger>
				<HoverCardContent>
					<div data-testid="card-body" style={{ overflow: "auto", height: 40 }}>
						preview
					</div>
				</HoverCardContent>
			</HoverCard>
		</div>
	);
}

describe("HoverCard scroll dismissal", () => {
	// https://github.com/OrchestratorInc/agent-orchestrator/issues/7694
	it("closes when the content around it scrolls, as a streaming chat does", () => {
		const onOpenChange = vi.fn();
		render(<Timeline onOpenChange={onOpenChange} />);
		expect(screen.getByText("preview")).toBeInTheDocument();
		fireEvent.scroll(screen.getByTestId("timeline"));
		expect(screen.queryByText("preview")).not.toBeInTheDocument();
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("closes when the window itself scrolls", () => {
		render(<Timeline />);
		fireEvent.scroll(window);
		expect(screen.queryByText("preview")).not.toBeInTheDocument();
	});

	it("stays open while the card's own content scrolls", () => {
		render(<Timeline />);
		fireEvent.scroll(screen.getByTestId("card-body"));
		expect(screen.getByText("preview")).toBeInTheDocument();
	});

	it("asks a controlled owner to close and leaves the state to it", () => {
		function Controlled() {
			const [open, setOpen] = useState(true);
			return (
				<div data-testid="timeline" style={{ overflow: "auto", height: 100 }}>
					<HoverCard open={open} onOpenChange={setOpen}>
						<HoverCardTrigger>trigger</HoverCardTrigger>
						<HoverCardContent>preview</HoverCardContent>
					</HoverCard>
				</div>
			);
		}
		render(<Controlled />);
		fireEvent.scroll(screen.getByTestId("timeline"));
		expect(screen.queryByText("preview")).not.toBeInTheDocument();
	});
});
