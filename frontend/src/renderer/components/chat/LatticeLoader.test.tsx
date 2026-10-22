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

	it("runs its trail clockwise round the outer eight dots, leaving the centre at rest", () => {
		render(<LatticeLoader />);
		const dots = [...screen.getByTestId("lattice-loader").children] as HTMLElement[];
		expect(dots).toHaveLength(9);

		// Row-major cells, read in the order the trail reaches them.
		const steps = dots.map((dot) => dot.dataset.trailStep);
		expect(steps).toEqual(["0", "1", "2", "7", undefined, "3", "6", "5", "4"]);

		// Each step starts an eighth of a lap after the one before, all already
		// under way when the lattice mounts.
		const delays = [0, 1, 2, 5, 8, 7, 6, 3].map((cell) => Number.parseInt(dots[cell]!.style.animationDelay, 10));
		expect(delays).toEqual([-880, -770, -660, -550, -440, -330, -220, -110]);
	});
});
