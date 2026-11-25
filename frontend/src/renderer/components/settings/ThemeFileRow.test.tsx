import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CustomTheme } from "../../lib/custom-theme";

const { themesMock, importMutate, removeMock } = vi.hoisted(() => ({
	themesMock: vi.fn(),
	importMutate: vi.fn(),
	removeMock: vi.fn(),
}));

vi.mock("../../hooks/useCustomTheme", () => ({
	useCustomThemes: () => ({ data: { themes: themesMock(), switch: { themeId: "", mode: "", id: "" } } }),
	useImportCustomTheme: () => ({ mutate: importMutate, reset: vi.fn(), isPending: false }),
	useDeleteCustomTheme: () => ({ remove: removeMock, removing: false, error: undefined }),
}));

import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { ThemeFileRow } from "./ThemeFileRow";

const sumi: CustomTheme = {
	id: "sumi",
	name: "Sumi",
	appearance: "dark",
	colors: { canvas: "#101012" },
	variants: { light: { canvas: "#fafafa" } },
	css: "",
	updatedAt: "2026-11-19T10:00:00Z",
};

function renderRow() {
	const user = userEvent.setup();
	const writeText = vi.fn().mockResolvedValue(undefined);
	Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
	render(
		<TooltipProvider>
			<ThemeFileRow />
		</TooltipProvider>,
	);
	return { user, writeText };
}

beforeEach(() => {
	themesMock.mockReset().mockReturnValue([]);
	importMutate.mockReset();
	removeMock.mockReset();
	useUiStore.setState({ themeStyle: "nord" });
});

describe("ThemeFileRow", () => {
	it("copies a built-in theme as a file to start from, and offers no edit or delete", async () => {
		const { user, writeText } = renderRow();
		expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();

		await user.click(screen.getByRole("button", { name: "Copy file" }));
		await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
		const file = JSON.parse(writeText.mock.calls[0][0] as string);
		expect(file).toMatchObject({ version: 1, id: "nord-copy", appearance: "dark" });
		expect(file.colors.canvas).toMatch(/^#/);
		expect(file.variants.light.canvas).toMatch(/^#/);
		expect(await screen.findByText("Copied")).toBeTruthy();
	});

	it("imports a pasted theme file", async () => {
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Import" }));
		const field = await screen.findByRole("textbox", { name: "Theme file" });
		await user.click(field);
		await user.paste('{"version":1,"name":"One","appearance":"dark","colors":{"canvas":"#000"}}');
		const dialog = screen.getByRole("dialog");
		await user.click(Array.from(dialog.querySelectorAll("button")).find((button) => button.textContent === "Import") as HTMLElement);

		expect(importMutate).toHaveBeenCalledTimes(1);
		// An import never replaces a theme without saying so first.
		expect(importMutate.mock.calls[0][0]).toEqual({
			file: { version: 1, name: "One", appearance: "dark", colors: { canvas: "#000" } },
			keepExisting: true,
		});
	});

	it("asks before replacing a theme that has the same id, then replaces on the second confirm", async () => {
		importMutate.mockImplementationOnce((_variables, options) => options.onError({ code: "CUSTOM_THEME_EXISTS", message: "exists" }));
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Import" }));
		const field = await screen.findByRole("textbox", { name: "Theme file" });
		await user.click(field);
		await user.paste('{"version":1,"name":"One","appearance":"dark","colors":{"canvas":"#000"}}');
		const dialog = screen.getByRole("dialog");
		await user.click(Array.from(dialog.querySelectorAll("button")).find((button) => button.textContent === "Import") as HTMLElement);

		expect((await screen.findByRole("alert")).textContent).toContain("already in your list");
		await user.click(screen.getByRole("button", { name: "Replace" }));
		expect(importMutate).toHaveBeenCalledTimes(2);
		expect(importMutate.mock.calls[1][0]).toMatchObject({ keepExisting: false });
	});

	it("says what an import left out of the file", async () => {
		importMutate.mockImplementationOnce((_variables, options) =>
			options.onSuccess({ theme: { ...sumi, id: "one" }, themes: [], switch: { themeId: "", mode: "", id: "" }, warnings: ['left out "sidebarBg": not a color role'] }),
		);
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Import" }));
		const field = await screen.findByRole("textbox", { name: "Theme file" });
		await user.click(field);
		await user.paste('{"version":1,"name":"One","appearance":"dark","colors":{"canvas":"#000","sidebarBg":"#000"}}');
		const dialog = screen.getByRole("dialog");
		await user.click(Array.from(dialog.querySelectorAll("button")).find((button) => button.textContent === "Import") as HTMLElement);

		expect((await screen.findByRole("status")).textContent).toContain('left out "sidebarBg"');
	});

	it("refuses a chosen file that is too large to be a theme", async () => {
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Import" }));
		await screen.findByRole("textbox", { name: "Theme file" });
		const input = screen.getByRole("dialog").querySelector('input[type="file"]') as HTMLInputElement;
		const huge = new File(["{}"], "big.json", { type: "application/json" });
		Object.defineProperty(huge, "size", { value: 5 * 1024 * 1024 });
		await user.upload(input, huge);

		expect((await screen.findByRole("alert")).textContent).toContain("too large");
		expect(importMutate).not.toHaveBeenCalled();
	});

	it("says so when the text is not a theme file, and sends nothing", async () => {
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Import" }));
		const field = await screen.findByRole("textbox", { name: "Theme file" });
		await user.click(field);
		await user.paste("[1, 2]");
		const dialog = screen.getByRole("dialog");
		await user.click(Array.from(dialog.querySelectorAll("button")).find((button) => button.textContent === "Import") as HTMLElement);

		expect(importMutate).not.toHaveBeenCalled();
		expect((await screen.findByRole("alert")).textContent).toContain("not a theme file");
	});

	it("edits the custom theme on screen from its file", async () => {
		themesMock.mockReturnValue([sumi]);
		useUiStore.setState({ themeStyle: "custom:sumi" });
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Edit" }));
		const field = (await screen.findByRole("textbox", { name: "Theme file" })) as HTMLTextAreaElement;
		expect(JSON.parse(field.value)).toEqual({
			version: 1,
			id: "sumi",
			name: "Sumi",
			appearance: "dark",
			colors: { canvas: "#101012" },
			variants: { light: { canvas: "#fafafa" } },
		});
		await user.click(screen.getByRole("button", { name: "Save" }));
		// Saving an edit replaces the theme being edited.
		expect(importMutate.mock.calls[0][0]).toMatchObject({ file: { id: "sumi", name: "Sumi" }, keepExisting: false });
	});

	it("saves an edit to the theme that was opened, whatever id is typed into the file", async () => {
		themesMock.mockReturnValue([sumi, { ...sumi, id: "other", name: "Other" }]);
		useUiStore.setState({ themeStyle: "custom:sumi" });
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Edit" }));
		const field = (await screen.findByRole("textbox", { name: "Theme file" })) as HTMLTextAreaElement;
		await user.clear(field);
		await user.click(field);
		await user.paste(JSON.stringify({ version: 1, id: "other", name: "Sumi", appearance: "dark", colors: { canvas: "#000000" } }));
		await user.click(screen.getByRole("button", { name: "Save" }));
		// The daemon stores it under the id sent beside the file.
		expect(importMutate.mock.calls[0][0]).toMatchObject({ id: "sumi", keepExisting: false });
	});

	it("deletes the theme the dialog was opened for, even if the theme on screen changes meanwhile", async () => {
		themesMock.mockReturnValue([sumi, { ...sumi, id: "other", name: "Other" }]);
		useUiStore.setState({ themeStyle: "custom:sumi" });
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Delete" }));
		const dialog = await screen.findByRole("dialog");
		// An agent switches the window to another custom theme while the dialog is open.
		act(() => useUiStore.setState({ themeStyle: "custom:other" }));
		expect(dialog.textContent).toContain("Sumi will be removed");
		await user.click(Array.from(dialog.querySelectorAll("button")).find((button) => button.textContent === "Delete") as HTMLElement);
		expect(removeMock).toHaveBeenCalledWith("sumi");
	});

	it("asks before deleting a custom theme", async () => {
		themesMock.mockReturnValue([sumi]);
		useUiStore.setState({ themeStyle: "custom:sumi" });
		const { user } = renderRow();
		await user.click(screen.getByRole("button", { name: "Delete" }));
		expect(removeMock).not.toHaveBeenCalled();
		const dialog = await screen.findByRole("dialog");
		expect(dialog.textContent).toContain("Sumi will be removed");
		await user.click(Array.from(dialog.querySelectorAll("button")).find((button) => button.textContent === "Delete") as HTMLElement);
		expect(removeMock).toHaveBeenCalledWith("sumi");
	});
});
