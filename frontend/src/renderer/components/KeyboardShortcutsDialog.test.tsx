import { fireEvent, render as renderUi, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { KeyboardShortcutsDialog } from "./KeyboardShortcutsDialog";
import { APP_SHORTCUTS } from "../../shared/shortcuts";
import { shortcutLabelKeys } from "../i18n/key-maps";
import { enMessages } from "../i18n/messages";
import type { ReactNode } from "react";
import { TooltipProvider } from "./ui/tooltip";
import { useKeybindingsStore } from "../stores/keybindings-store";

function render(ui: ReactNode) {
	return renderUi(<TooltipProvider>{ui}</TooltipProvider>);
}

const ctx = vi.hoisted(() => ({ commandPaletteEnabled: true }));

vi.mock("../hooks/useCommandPaletteEnabled", () => ({
	useCommandPaletteEnabled: () => ctx.commandPaletteEnabled,
}));

describe("KeyboardShortcutsDialog", () => {
	beforeEach(() => {
		ctx.commandPaletteEnabled = true;
		useKeybindingsStore.setState({ overrides: {}, loaded: true });
	});

	it("shows every shortcut grouped by purpose, with Windows/Linux keys", () => {
		render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} isMac={false} />);

		expect(screen.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeInTheDocument();
		for (const heading of ["General", "Navigation", "Session", "Panels", "Chat, terminal and browser"]) {
			expect(screen.getByRole("heading", { name: heading })).toBeInTheDocument();
		}
		// Header carries the dialog's own chord.
		expect(screen.getByLabelText("Ctrl + /")).toBeInTheDocument();
		expect(screen.getByLabelText("Ctrl + PageUp")).toBeInTheDocument();
		expect(screen.getByLabelText("Ctrl + PageDown")).toBeInTheDocument();
		expect(screen.getByLabelText("Alt + Left")).toBeInTheDocument();
		expect(screen.getByLabelText("Ctrl + `")).toBeInTheDocument();
	});

	it("lists every customizable and fixed command exactly once", () => {
		render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} isMac />);

		for (const { id } of APP_SHORTCUTS) {
			if (id === "keyboard-shortcuts") continue;
			const label = enMessages[shortcutLabelKeys[id] as keyof typeof enMessages] as string;
			expect(screen.getAllByText(label), `${id} (${label})`).toHaveLength(1);
		}
		expect(screen.getByText("Jump to session 1–9")).toBeInTheDocument();
	});

	it("uses macOS symbols in Apple's modifier order and spells them out for screen readers", () => {
		render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} isMac />);

		expect(screen.getByLabelText("Command + /")).toBeInTheDocument();
		expect(screen.getByLabelText("Shift + Command + B")).toBeInTheDocument();
		expect(screen.getByLabelText("Option + Shift + Command + Down")).toBeInTheDocument();
		expect(screen.getByLabelText("Control + `")).toBeInTheDocument();
		const inspectorRow = screen.getByText("Toggle inspector").closest("li");
		expect(within(inspectorRow as HTMLElement).getByText("⇧")).toBeInTheDocument();
		expect(within(inspectorRow as HTMLElement).getAllByText("⌘")).toHaveLength(1);
		expect(within(inspectorRow as HTMLElement).getByText("+1")).toBeInTheDocument();
		expect(within(inspectorRow as HTMLElement).getByRole("img", { name: "1 alternative shortcut. Choose Customize to view all bindings." })).toBeInTheDocument();
		expect(within(inspectorRow as HTMLElement).queryByRole("img", { name: "Option + Command + B" })).not.toBeInTheDocument();
		const indexedRow = screen.getByText("Jump to session 1–9").closest("li") as HTMLElement;
		expect(within(indexedRow).getByRole("img", { name: "Command + 1 through 9" })).toBeInTheDocument();
		expect(within(indexedRow).queryByText(/^\+/)).not.toBeInTheDocument();
	});

	it("lists the fixed chat, terminal and browser keys for the platform", () => {
		const { unmount } = render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} isMac />);
		expect(within(screen.getByText("Send message").closest("li") as HTMLElement).getByText("Enter")).toBeInTheDocument();
		expect(within(screen.getByText("Stop the agent").closest("li") as HTMLElement).getByText("Esc")).toBeInTheDocument();
		expect(within(screen.getByText("Steer running agent").closest("li") as HTMLElement).getByText("⌘")).toBeInTheDocument();
		unmount();

		render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} isMac={false} />);
		expect(within(screen.getByText("Steer running agent").closest("li") as HTMLElement).getByText("Ctrl")).toBeInTheDocument();
	});

	it("hides the palette shortcuts when the feature is disabled", () => {
		ctx.commandPaletteEnabled = false;
		render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} isMac={false} />);

		expect(screen.queryByText("Open command palette")).not.toBeInTheDocument();
		expect(screen.queryByText("Quick open file")).not.toBeInTheDocument();
		expect(screen.getByText("Go back")).toBeInTheDocument();
	});

	it("offers a direct path to customize shortcuts", () => {
		const onCustomize = vi.fn();
		render(<KeyboardShortcutsDialog open onOpenChange={vi.fn()} onCustomize={onCustomize} isMac={false} />);

		fireEvent.click(screen.getByRole("button", { name: "Customize" }));

		expect(onCustomize).toHaveBeenCalledTimes(1);
	});

	it("closes from the close button", () => {
		const onOpenChange = vi.fn();
		render(<KeyboardShortcutsDialog open onOpenChange={onOpenChange} isMac />);

		fireEvent.click(screen.getByRole("button", { name: "Close" }));

		expect(onOpenChange).toHaveBeenCalledWith(false);
	});
});
