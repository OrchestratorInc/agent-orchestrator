import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SettingsSaveFooter } from "./SettingsSaveFooter";

const longError = "The control plane refused the change: repository acme/web is still used by Setup step 3, Cleanup step 1 and the session template, so it cannot be removed until those steps move to another repository.";

afterEach(() => vi.restoreAllMocks());

function clampCutsText(cut: boolean) {
	vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(cut ? 48 : 32);
	vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(32);
}

it("lets a keyboard user reach a clamped error and read all of it", async () => {
	clampCutsText(true);
	render(<form><SettingsSaveFooter dirty disabled={false} error={longError} /></form>);
	const alert = screen.getByRole("alert");
	expect(alert).toHaveTextContent(longError);
	expect(alert).toHaveClass("line-clamp-2");
	expect(alert).toHaveAttribute("tabindex", "0");
	act(() => alert.focus());
	expect(await screen.findByRole("tooltip")).toHaveTextContent(longError);
});

it("keeps an error that fits out of the tab order and without a tooltip", () => {
	clampCutsText(false);
	render(<form><SettingsSaveFooter dirty disabled={false} error="Save failed." /></form>);
	const alert = screen.getByRole("alert");
	expect(alert).not.toHaveAttribute("tabindex");
	act(() => alert.focus());
	expect(screen.queryByRole("tooltip")).toBeNull();
});

it("keeps the tooltip controlled when the error starts or stops being clamped", () => {
	const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
	let cut = false;
	vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => (cut ? 48 : 32));
	vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(32);
	const view = render(<form><SettingsSaveFooter dirty disabled={false} error="Save failed." /></form>);
	cut = true;
	view.rerender(<form><SettingsSaveFooter dirty disabled={false} error={longError} /></form>);
	expect(screen.getByRole("alert")).toHaveAttribute("tabindex", "0");
	expect(warn.mock.calls.flat().join(" ")).not.toMatch(/changing from (controlled|uncontrolled)/);
});
