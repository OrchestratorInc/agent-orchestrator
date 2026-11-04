import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GlobalToast } from "./GlobalToast";
import { useUiStore } from "../stores/ui-store";

describe("GlobalToast", () => {
	afterEach(() => {
		vi.useRealTimers();
		useUiStore.getState().clearGlobalToast();
	});

	it("stacks concurrent notifications and marks errors as alerts", () => {
		const { showGlobalToast } = useUiStore.getState();
		showGlobalToast("First", "Keep this visible");
		showGlobalToast("Second", "Something failed", "error");

		render(<GlobalToast />);

		expect(screen.getAllByRole("status")).toHaveLength(1);
		expect(screen.getAllByRole("alert")).toHaveLength(1);
		expect(screen.getByText("First")).toBeInTheDocument();
		expect(screen.getByText("Second")).toBeInTheDocument();
	});

	it("says an error toast came from AO Cloud, with the start of its request id and a copy of all of it", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		const before = window.ao!.clipboard.writeText;
		window.ao!.clipboard.writeText = writeText;
		try {
			useUiStore.getState().showGlobalToast("Couldn't start the session", undefined, {
				tone: "error",
				reference: { origin: "network", requestId: "0f8fad5b-d9cb-469f-a165-70867728950e" },
			});
			render(<GlobalToast />);
			const alert = screen.getByRole("alert");
			expect(alert).toHaveTextContent("Can't reach AO Cloud");
			expect(alert).toHaveTextContent("ref 0f8fad5b");
			fireEvent.click(screen.getByRole("button", { name: "Copy full request ID" }));
			await act(async () => undefined);
			expect(writeText).toHaveBeenCalledWith("0f8fad5b-d9cb-469f-a165-70867728950e");
		} finally {
			window.ao!.clipboard.writeText = before;
		}
	});

	it("pauses auto-dismiss while hovered and keeps errors up longer", () => {
		vi.useFakeTimers();
		const store = useUiStore.getState();
		store.showGlobalToast("Plain");
		store.showGlobalToast("Broken", undefined, "error");
		render(<GlobalToast />);

		fireEvent.mouseEnter(screen.getByRole("status"));
		act(() => { vi.advanceTimersByTime(4_000); });
		expect(screen.getByText("Plain")).toBeInTheDocument();
		expect(screen.getByText("Broken")).toBeInTheDocument();

		fireEvent.mouseLeave(screen.getByRole("status"));
		act(() => { vi.advanceTimersByTime(3_600); });
		expect(screen.queryByText("Plain")).not.toBeInTheDocument();
		expect(screen.getByText("Broken")).toBeInTheDocument();
	});

	it("keeps toast keys unique after dismissing the newest toast", () => {
		const store = useUiStore.getState();
		store.showGlobalToast("First");
		store.showGlobalToast("Second");
		store.dismissGlobalToast(2);
		store.showGlobalToast("Third");

		expect(useUiStore.getState().globalToasts.map((toast) => toast.nonce)).toEqual([1, 3]);
	});

	it("offers manual dismissal when requested", () => {
		useUiStore.getState().showGlobalToast("Dismiss me", undefined, { dismissible: true });
		render(<GlobalToast />);

		fireEvent.click(screen.getByRole("button", { name: "Dismiss notification" }));
		expect(screen.queryByText("Dismiss me")).not.toBeInTheDocument();
	});

	it("replaces a keyed toast and restarts its custom dismissal timer", () => {
		vi.useFakeTimers();
		const store = useUiStore.getState();
		store.showGlobalToast("Session stopped", undefined, { dedupeKey: "session:one", durationMs: 5_000 });
		render(<GlobalToast />);

		act(() => vi.advanceTimersByTime(4_000));
		act(() =>
			useUiStore.getState().showGlobalToast("Session stopped", undefined, { dedupeKey: "session:one", durationMs: 5_000 }),
		);
		expect(useUiStore.getState().globalToasts).toHaveLength(1);
		expect(useUiStore.getState().globalToasts[0]?.nonce).toBe(2);

		act(() => vi.advanceTimersByTime(1_000));
		expect(screen.getByText("Session stopped")).toBeInTheDocument();
		act(() => vi.advanceTimersByTime(4_000));
		expect(screen.queryByText("Session stopped")).not.toBeInTheDocument();
	});
});
