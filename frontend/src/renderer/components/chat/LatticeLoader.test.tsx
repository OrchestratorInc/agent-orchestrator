import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { LatticeLoader } from "./LatticeLoader";

// The lattice is decoration beside a label that says what the agent is doing, so
// the things worth pinning are that assistive tech skips it and that the trail
// runs round the edge in order rather than flashing the grid at random.
describe("LatticeLoader", () => {
	it("is hidden from assistive tech", () => {
		render(<LatticeLoader />);
		expect(screen.getByTestId("lattice-loader")).toHaveAttribute("aria-hidden", "true");
	});

	it("runs its trail clockwise round the outer twelve dots, leaving the inner four at rest", () => {
		render(<LatticeLoader />);
		const dots = [...screen.getByTestId("lattice-loader").children] as HTMLElement[];
		expect(dots).toHaveLength(16);

		// Row-major cells, read in the order the trail reaches them.
		const steps = dots.map((dot) => dot.dataset.trailStep);
		expect(steps).toEqual([
			"0", "1", "2", "3",
			"11", undefined, undefined, "4",
			"10", undefined, undefined, "5",
			"9", "8", "7", "6",
		]);

		// Each step starts a twelfth of a lap after the one before, all already
		// under way when the lattice mounts.
		const delays = [0, 1, 2, 3, 7, 11, 15, 14, 13, 12, 8, 4].map((cell) =>
			Number.parseInt(dots[cell]!.style.animationDelay, 10),
		);
		expect(delays).toEqual([-1200, -1100, -1000, -900, -800, -700, -600, -500, -400, -300, -200, -100]);
	});
});
