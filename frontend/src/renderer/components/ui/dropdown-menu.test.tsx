import { useState, type RefCallback } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { SessionFileTabs } from "../SessionFileTabs";
import { DropdownMenu, DropdownMenuTrigger } from "./dropdown-menu";
import { TooltipProvider } from "./tooltip";

const triggerNames = [
	"Session actions",
	"Model and reasoning effort for next turn",
	"Approval policy for next turn",
	"More",
];

function FullFileMenuComposition({ triggerRefs }: { triggerRefs: RefCallback<HTMLButtonElement>[] }) {
	const [path, setPath] = useState<string | null>(null);
	return (
		<TooltipProvider>
			{triggerNames.map((name, index) => (
				<DropdownMenu key={name}>
					<DropdownMenuTrigger asChild ref={triggerRefs[index]}>
						<button type="button">{name}</button>
					</DropdownMenuTrigger>
				</DropdownMenu>
			))}
			<button onClick={() => setPath("README.md")} type="button">Open README full file</button>
			{path ? (
				<>
					<div role="tablist">
						<SessionFileTabs
							onActivateFile={() => {}}
							onCloseFile={() => setPath(null)}
							state={{ activePath: path, openPaths: [path] }}
						/>
					</div>
					<main>{path}</main>
				</>
			) : null}
		</TooltipProvider>
	);
}

it("keeps existing menu anchors attached while a full file and its delayed tooltip mount", async () => {
	const triggerRefs = triggerNames.map(() => vi.fn<(element: HTMLButtonElement | null) => void>());
	render(<FullFileMenuComposition triggerRefs={triggerRefs} />);
	const triggers = triggerNames.map((name) => screen.getByRole("button", { name }));

	for (const [index, triggerRef] of triggerRefs.entries()) {
		expect(triggerRef.mock.calls).toEqual([[triggers[index]]]);
	}

	await userEvent.click(screen.getByRole("button", { name: "Open README full file" }));
	expect(screen.getByRole("main")).toHaveTextContent("README.md");
	await userEvent.hover(screen.getByRole("button", { name: "Close README.md" }));
	expect(await screen.findByRole("tooltip")).toHaveTextContent("Close README.md");

	for (const [index, triggerRef] of triggerRefs.entries()) {
		expect(triggerRef.mock.calls).toEqual([[triggers[index]]]);
	}
});
