import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { KeybindingOverrides } from "../../../shared/shortcuts";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { TooltipProvider } from "../ui/tooltip";
import { KeyboardShortcutsSettingsDialog } from "./KeyboardShortcutsSettingsDialog";

const persistBindings = vi.fn(async (overrides: KeybindingOverrides) => overrides);
const setRecording = vi.fn(async () => undefined);

function renderDialog(isMac = false) {
	return render(
		<TooltipProvider>
			<KeyboardShortcutsSettingsDialog open onOpenChange={vi.fn()} isMac={isMac} />
		</TooltipProvider>,
	);
}

describe("KeyboardShortcutsSettingsDialog", () => {
	beforeEach(() => {
		persistBindings.mockClear();
		setRecording.mockClear();
		window.ao!.keybindings.set = persistBindings;
		window.ao!.keybindings.setRecording = setRecording;
		useKeybindingsStore.setState({ overrides: {}, loaded: true });
	});

	it("records an application-owned chord and makes Undo keyboard-accessible in the dialog", async () => {
		const user = userEvent.setup();
		renderDialog();

		await user.click(screen.getByRole("button", { name: "Change New session" }));
		await waitFor(() => expect(setRecording).toHaveBeenCalledWith(true));

		fireEvent.keyDown(window, { key: "j", code: "KeyJ", ctrlKey: true });

		const toast = await screen.findByRole("status");
		expect(toast).toHaveTextContent("Shortcut updated");
		expect(useKeybindingsStore.getState().overrides["new-session"]).toEqual([
			{ key: "j", ctrl: true, meta: false, shift: false, alt: false },
		]);
		expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Undo" }));
		await waitFor(() => expect(useKeybindingsStore.getState().overrides).toEqual({}));
		expect(setRecording).toHaveBeenCalledWith(false);
	});

	it("opens a real modal for conflicts, supports Escape, and can reassign the chord", async () => {
		const user = userEvent.setup();
		renderDialog();

		await user.click(screen.getByRole("button", { name: "Change New session" }));
		fireEvent.keyDown(window, { key: "/", code: "Slash", ctrlKey: true });

		expect(await screen.findByRole("dialog", { name: "Shortcut already in use" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reassign" })).toBeEnabled();
		await user.keyboard("{Escape}");
		await waitFor(() =>
			expect(screen.queryByRole("dialog", { name: "Shortcut already in use" })).not.toBeInTheDocument(),
		);
		expect(persistBindings).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Change New session" }));
		fireEvent.keyDown(window, { key: "/", code: "Slash", ctrlKey: true });
		await user.click(await screen.findByRole("button", { name: "Reassign" }));

		await waitFor(() => {
			expect(useKeybindingsStore.getState().overrides["new-session"]?.[0]?.key).toBe("/");
			expect(useKeybindingsStore.getState().overrides["keyboard-shortcuts"]).toEqual([]);
		});
	});

	it("ignores AltGraph/lock keys, blocks dangerous chords, and releases recording on blur", async () => {
		const user = userEvent.setup();
		renderDialog();

		await user.click(screen.getByRole("button", { name: "Change Toggle terminal" }));
		fireEvent.keyDown(window, { key: "AltGraph", ctrlKey: true, altKey: true });
		fireEvent.keyDown(window, { key: "CapsLock" });
		expect(persistBindings).not.toHaveBeenCalled();

		fireEvent.keyDown(window, { key: "c", code: "KeyC", ctrlKey: true });
		expect(await screen.findByRole("status")).toHaveTextContent("Shortcut is reserved");
		expect(screen.getByText(/Press shortcut/)).toBeInTheDocument();

		fireEvent.blur(window);
		await waitFor(() => expect(setRecording).toHaveBeenLastCalledWith(false));
		expect(screen.queryByText(/Press shortcut/)).not.toBeInTheDocument();
	});

	it("adds, removes, resets, and resets all bindings", async () => {
		const user = userEvent.setup();
		useKeybindingsStore.setState({
			overrides: {
				"toggle-terminal": [{ key: "j", ctrl: true, meta: false, shift: false, alt: false }],
				"toggle-sidebar": [{ key: "s", ctrl: false, meta: false, shift: false, alt: true }],
			},
			loaded: true,
		});
		renderDialog();

		await user.click(screen.getByRole("button", { name: "Add alternative for Toggle terminal" }));
		fireEvent.keyDown(window, { key: "k", code: "KeyK", altKey: true });
		await waitFor(() => expect(useKeybindingsStore.getState().overrides["toggle-terminal"]).toHaveLength(2));

		await user.click(screen.getByRole("button", { name: "Remove Ctrl+J from Toggle terminal" }));
		await waitFor(() => expect(useKeybindingsStore.getState().overrides["toggle-terminal"]).toHaveLength(1));

		await user.click(screen.getByRole("button", { name: "Reset Toggle terminal" }));
		await waitFor(() =>
			expect(Object.hasOwn(useKeybindingsStore.getState().overrides, "toggle-terminal")).toBe(false),
		);

		await user.click(screen.getByRole("button", { name: "Reset all" }));
		await user.click(screen.getByRole("button", { name: "Reset all" }));
		await waitFor(() => expect(useKeybindingsStore.getState().overrides).toEqual({}));
	});

	it("keeps the physical code for bracket chords", async () => {
		const user = userEvent.setup();
		renderDialog(true);

		await user.click(screen.getByRole("button", { name: "Change Toggle terminal" }));
		fireEvent.keyDown(window, { key: "]", code: "BracketRight", ctrlKey: true, altKey: true });

		await waitFor(() =>
			expect(useKeybindingsStore.getState().overrides["toggle-terminal"]).toEqual([
				{ key: "]", code: "BracketRight", ctrl: true, meta: false, shift: false, alt: true },
			]),
		);
	});

	it("keeps the physical code for Option-letter chords, whose key is a composed character", async () => {
		const user = userEvent.setup();
		renderDialog(true);

		await user.click(screen.getByRole("button", { name: "Change Toggle terminal" }));
		fireEvent.keyDown(window, { key: "∫", code: "KeyB", altKey: true });

		await waitFor(() =>
			expect(useKeybindingsStore.getState().overrides["toggle-terminal"]).toEqual([
				{ key: "∫", code: "KeyB", ctrl: false, meta: false, shift: false, alt: true },
			]),
		);
	});

	it("does not attach a code to a plain letter chord", async () => {
		const user = userEvent.setup();
		renderDialog(true);

		await user.click(screen.getByRole("button", { name: "Change Toggle terminal" }));
		fireEvent.keyDown(window, { key: "J", code: "KeyJ", metaKey: true, shiftKey: true });

		await waitFor(() =>
			expect(useKeybindingsStore.getState().overrides["toggle-terminal"]).toEqual([
				{ key: "j", ctrl: false, meta: true, shift: true, alt: false },
			]),
		);
	});

	it("detects a conflict with the default previous-session chord (Cmd+Shift+[)", async () => {
		const user = userEvent.setup();
		renderDialog(true);

		await user.click(screen.getByRole("button", { name: "Change Toggle terminal" }));
		// With Shift held the browser reports "{", so only the code ties it to the default.
		fireEvent.keyDown(window, { key: "{", code: "BracketLeft", metaKey: true, shiftKey: true });

		expect(await screen.findByRole("dialog", { name: "Shortcut already in use" })).toBeInTheDocument();
		expect(persistBindings).not.toHaveBeenCalled();
	});

	it("uses the platform prop for displayed and recorded bindings", async () => {
		const user = userEvent.setup();
		renderDialog(true);

		expect(screen.getByText("⌘N")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Change Toggle terminal" }));
		fireEvent.keyDown(window, { key: "j", code: "KeyJ", metaKey: true });

		await waitFor(() =>
			expect(useKeybindingsStore.getState().overrides["toggle-terminal"]?.[0]?.meta).toBe(true),
		);
		expect(await screen.findByRole("status")).toHaveTextContent("⌘J");
	});
});
