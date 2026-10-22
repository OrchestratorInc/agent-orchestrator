import { describe, expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";

type QuitOptions = Parameters<typeof createDesktopQuitController>[0];

function setup(overrides: Partial<QuitOptions> = {}) {
	const options: QuitOptions = {
		cleanup: vi.fn(() => Promise.resolve()),
		finish: vi.fn(),
		exit: vi.fn(),
		log: vi.fn(),
		...overrides,
	};
	return { options, controller: createDesktopQuitController(options), event: { preventDefault: vi.fn() } };
}

describe("desktop quit", () => {
	it("defers the quit to run the cleanup instead of backgrounding to the tray", () => {
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(options.cleanup).toHaveBeenCalledOnce();
	});

	it("starts the shared cleanup once across repeated quit events", () => {
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		controller.handleBeforeQuit(event);
		expect(options.cleanup).toHaveBeenCalledOnce();
	});

	it("finishes quitting once the cleanup settles", async () => {
		let settle: (() => void) | undefined;
		const { options, controller } = setup({
			cleanup: vi.fn(() => new Promise<void>((resolve) => { settle = resolve; })),
		});
		controller.handleBeforeQuit({ preventDefault: () => undefined });
		settle?.();
		await vi.waitFor(() => expect(options.finish).toHaveBeenCalledOnce());
		expect(options.exit).not.toHaveBeenCalled();
	});

	it("force-exits when the cleanup outlives the deadline", async () => {
		vi.useFakeTimers();
		try {
			const { options, controller } = setup({ cleanup: vi.fn(() => new Promise<void>(() => undefined)) });
			controller.handleBeforeQuit({ preventDefault: () => undefined });
			await vi.advanceTimersByTimeAsync(15_000);
			expect(options.exit).toHaveBeenCalledOnce();
			expect(options.finish).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("still finishes the quit when the cleanup fails", async () => {
		const { options, controller } = setup({ cleanup: vi.fn(() => Promise.reject(new Error("dispose boom"))) });
		controller.handleBeforeQuit({ preventDefault: () => undefined });
		await vi.waitFor(() => expect(options.finish).toHaveBeenCalledOnce());
		expect(options.log).toHaveBeenCalledOnce();
	});
});
