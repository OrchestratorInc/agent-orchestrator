import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./hover-card";

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
