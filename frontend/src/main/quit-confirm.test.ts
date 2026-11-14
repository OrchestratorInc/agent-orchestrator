import { describe, expect, it, vi } from "vitest";
import { createQuitConfirmController } from "./quit-confirm";

function setup(canConfirm = true) {
	const requestConfirm = vi.fn();
	const quit = vi.fn();
	const controller = createQuitConfirmController({ canConfirm: () => canConfirm, requestConfirm, quit });
	return { controller, requestConfirm, quit };
}

describe("createQuitConfirmController", () => {
	it("asks the shell to confirm on ⌘Q and quits once confirmed", () => {
		const { controller, requestConfirm, quit } = setup();

		controller.menuQuit(true);
		expect(requestConfirm).toHaveBeenCalledOnce();
		expect(quit).not.toHaveBeenCalled();

		controller.resolve(true);
		expect(quit).toHaveBeenCalledOnce();
	});

	it("quits on a second ⌘Q while the dialog is showing", () => {
		const { controller, requestConfirm, quit } = setup();

		controller.menuQuit(true);
		controller.menuQuit(true);
		expect(requestConfirm).toHaveBeenCalledOnce();
		expect(quit).toHaveBeenCalledOnce();
	});

	it("asks again after the dialog was cancelled or the shell reloaded", () => {
		const { controller, requestConfirm, quit } = setup();

		controller.menuQuit(true);
		controller.resolve(false);
		controller.menuQuit(true);
		controller.reset();
		controller.menuQuit(true);
		expect(requestConfirm).toHaveBeenCalledTimes(3);
		expect(quit).not.toHaveBeenCalled();
	});

	it("quits at once from a mouse click or when there is no shell to ask in", () => {
		const clicked = setup();
		clicked.controller.menuQuit(false);
		expect(clicked.quit).toHaveBeenCalledOnce();
		expect(clicked.requestConfirm).not.toHaveBeenCalled();

		const hidden = setup(false);
		hidden.controller.menuQuit(true);
		expect(hidden.quit).toHaveBeenCalledOnce();
		expect(hidden.requestConfirm).not.toHaveBeenCalled();
	});
});
