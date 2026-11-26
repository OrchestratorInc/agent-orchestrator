import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GlobalToast } from "./GlobalToast";
import { showMove, useMoveStore } from "../lib/move-runner";
import { useUiStore } from "../stores/ui-store";

describe("GlobalToast", () => {
	it("shows a move's step only while no session view shows it", () => {
		useMoveStore.setState({
			moves: { "local:web-3": { phase: "moving", targetHostId: "host-b", targetProjectId: "web", targetLabel: "MacBook B", title: "Osprey notes", step: "copy" } },
			shown: {},
		});
		render(<GlobalToast />);
		expect(screen.getByText("Osprey notes")).toBeInTheDocument();
		expect(screen.getByText("Moving to MacBook B · Copying the work")).toBeInTheDocument();
		let hide = () => {};
		act(() => {
			hide = showMove("local:web-3");
		});
		expect(screen.queryByText("Osprey notes")).not.toBeInTheDocument();
		act(() => hide());
		useMoveStore.setState({ moves: {}, shown: {} });
	});

	it("keeps a persistent actionable notice until the user acts", () => {
		vi.useFakeTimers();
		const acknowledge = vi.fn();
		useUiStore.getState().showGlobalToast("Paused", undefined, { durationMs: 0, action: { label: "Acknowledge", run: acknowledge } });
		render(<GlobalToast />);
		act(() => vi.advanceTimersByTime(60_000));
		expect(screen.getByText("Paused")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Acknowledge" }));
		expect(acknowledge).toHaveBeenCalledOnce();
		expect(screen.queryByText("Paused")).not.toBeInTheDocument();
	});
	afterEach(() => {
		vi.useRealTimers();
		useUiStore.getState().clearGlobalToast();
	});

	it("shows compact centered success feedback with a green check for the existing duration", () => {
		vi.useFakeTimers();
		useUiStore.getState().showGlobalToast("Copied to clipboard", undefined, { tone: "success", placement: "bottom-center" });
		render(<GlobalToast />);
		const toast = screen.getByRole("status");
		expect(toast.parentElement).toHaveClass("left-1/2", "-translate-x-1/2", "bottom-3", "w-max");
		expect(toast).toHaveClass("gap-2", "py-2");
		expect(toast.querySelector("svg")).toHaveClass("icon-check", "text-green-500");
		act(() => vi.advanceTimersByTime(3_499));
		expect(screen.getByText("Copied to clipboard")).toBeInTheDocument();
		act(() => vi.advanceTimersByTime(1));
		expect(screen.queryByText("Copied to clipboard")).not.toBeInTheDocument();
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
