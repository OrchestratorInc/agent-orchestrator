import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SettingsRow } from "./SettingsRow";
import { SettingsPageTitleContext, SettingsSection } from "./SettingsSection";

describe("SettingsRow", () => {
	it("renders a node description under the label and passes row attributes through", () => {
		render(
			<SettingsSection title="Version" grouped>
				<SettingsRow
					data-agent="codex"
					id="row"
					label={<span id="row-label">v1.2.3</span>}
					leading={<span data-testid="avatar" />}
					description={<span role="status">Checked 2 minutes ago</span>}
				>
					<button type="button">Check for updates</button>
				</SettingsRow>
			</SettingsSection>,
		);
		const row = document.getElementById("row");
		expect(row).toHaveAttribute("data-agent", "codex");
		expect(row).toHaveClass("settings-row-bar");
		expect(row?.parentElement).toHaveClass("settings-grouped-rows");
		expect(screen.getByRole("status")).toHaveTextContent("Checked 2 minutes ago");
		expect(screen.getByTestId("avatar")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Check for updates" })).toBeInTheDocument();
	});

	it("gives the free space to the text and keeps the control at its own width", () => {
		render(
			<SettingsRow id="row" label="Label" description="A long description that wraps rather than running under the control">
				<button type="button">Action</button>
			</SettingsRow>,
		);
		const control = screen.getByRole("button", { name: "Action" }).parentElement;
		const text = control?.previousElementSibling;
		// The text column flexes; the control slot never shrinks, so it cannot spill over the text.
		expect(text).toHaveClass("min-w-0", "flex-1");
		expect(control).toHaveClass("shrink-0");
		expect(control).not.toHaveClass("flex-1");
	});

	it("truncates a label that has no description", () => {
		render(<SettingsRow label="A very long paired device name from someone's phone" />);
		const label = screen.getByText("A very long paired device name from someone's phone");
		expect(label).toHaveClass("min-w-0", "truncate");
		expect(label).not.toHaveClass("whitespace-nowrap");
		expect(label.parentElement).toHaveClass("min-w-0", "flex-1");
	});
});

describe("SettingsSection", () => {
	it("renders the toolbar between the heading and the card", () => {
		render(
			<SettingsSection title="Downloads" grouped toolbar={<div data-testid="toolbar" />}>
				<SettingsRow label="Row" />
			</SettingsSection>,
		);
		const heading = screen.getByRole("heading", { name: "Downloads" });
		const toolbar = screen.getByTestId("toolbar");
		const card = toolbar.nextElementSibling;
		expect(heading.compareDocumentPosition(toolbar) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(card).toHaveClass("settings-grouped-rows");
		expect(card).not.toContainElement(toolbar);
	});

	it("hides only a heading equal to the page title", () => {
		render(
			<SettingsPageTitleContext.Provider value="Coder">
				<SettingsSection title=" coder " grouped><SettingsRow label="Row" /></SettingsSection>
				<SettingsSection title="Templates on your Coder" grouped><SettingsRow label="Row" /></SettingsSection>
			</SettingsPageTitleContext.Provider>,
		);
		expect(screen.queryByRole("heading", { name: "coder" })).not.toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Templates on your Coder" })).toBeInTheDocument();
	});

	it("renders a heading action on the heading line, outside the card", () => {
		render(
			<SettingsSection title="Machine" testId="machine" grouped action={<button type="button">Copy report</button>}>
				<SettingsRow label="Row" />
			</SettingsSection>,
		);
		const heading = screen.getByRole("heading", { name: "Machine" });
		const action = screen.getByRole("button", { name: "Copy report" });
		expect(heading.parentElement).toContainElement(action);
		expect(screen.getByTestId("machine").querySelector(".settings-grouped-rows")).not.toContainElement(action);
	});
});
