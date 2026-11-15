import { fireEvent, render, screen } from "@testing-library/react";
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
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
		expect(screen.getByText("sibling stays")).toBeInTheDocument();
	});

	it("offers to retry the panel before a full reload when it can reset", () => {
		const reset = vi.fn();
		render(<LazyChunkErrorPanel reset={reset} />);
		fireEvent.click(screen.getByRole("button", { name: "Try again" }));
		expect(reset).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
	});

	it("does not offer a retry that cannot recover", () => {
		render(<LazyChunkErrorPanel />);
		expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
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
