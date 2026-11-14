import { fireEvent, render, screen } from "@testing-library/react";
import { CatchBoundary } from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LazyChunkBoundary, LazyChunkErrorPanel } from "./LazyChunkBoundary";

vi.mock("../lib/telemetry", () => ({ captureRendererException: vi.fn() }));

describe("LazyChunkBoundary", () => {
	afterEach(() => vi.restoreAllMocks());

	it("contains a failed lazy import to its own panel", async () => {
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		const Broken = lazy(() => Promise.reject(new Error("chunk failed to load")));
		render(
			<div>
				<span>sibling stays</span>
				<LazyChunkBoundary>
					<Suspense fallback={null}>
						<Broken />
					</Suspense>
				</LazyChunkBoundary>
			</div>,
		);
		expect(await screen.findByRole("alert")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reload window" })).toBeInTheDocument();
		expect(screen.getByText("sibling stays")).toBeInTheDocument();
		expect(screen.getByText("chunk failed to load")).toBeInTheDocument();
		expect(screen.getByText(/include the error details in a bug report/)).toBeInTheDocument();
	});

	it("offers to retry the panel before a full reload when it can reset", () => {
		const reset = vi.fn();
		render(<LazyChunkErrorPanel reset={reset} error={new Error("Session view failed")} />);
		expect(screen.getByText(/without closing other panels/)).toBeInTheDocument();
		expect(screen.getByText("Error details")).toBeInTheDocument();
		expect(screen.getByText("Session view failed")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Reload this view" }));
		expect(reset).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Reload window" })).toBeInTheDocument();
	});

	it("does not offer a retry that cannot recover", () => {
		render(<LazyChunkErrorPanel />);
		expect(screen.queryByRole("button", { name: "Reload this view" })).not.toBeInTheDocument();
		expect(screen.queryByText("Error details")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reload window" })).toBeInTheDocument();
	});

	it("shows a caught session error and resets only the failed view", () => {
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		let broken = true;
		function Session() {
			if (broken) throw new Error("Session render failed");
			return <span>session recovered</span>;
		}
		render(<div>
			<span>other panel</span>
			<CatchBoundary errorComponent={LazyChunkErrorPanel} getResetKey={() => "session"}>
				<Session />
			</CatchBoundary>
		</div>);
		expect(screen.getByText("Session render failed")).toBeInTheDocument();
		broken = false;
		fireEvent.click(screen.getByRole("button", { name: "Reload this view" }));
		expect(screen.getByText("session recovered")).toBeInTheDocument();
		expect(screen.getByText("other panel")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("shows string errors as text rather than markup", () => {
		render(<LazyChunkErrorPanel error="<b>Failed to load view</b>" />);
		expect(screen.getByText("<b>Failed to load view</b>")).toBeInTheDocument();
		expect(screen.getByRole("alert").querySelector("b")).toBeNull();
	});

	it("renders its children when nothing fails", () => {
		render(
			<LazyChunkBoundary>
				<span>terminal</span>
			</LazyChunkBoundary>,
		);
		expect(screen.getByText("terminal")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});
});
