import { render, screen } from "@testing-library/react";
import { GitPullRequest } from "lucide-react";
import { describe, expect, it } from "vitest";
import { InfoRow, InfoRowLabelButton } from "./InspectorInfoRow";

// jsdom has no layout, so this pins the class that keeps an absolutely
// positioned sr-only suffix inside the label's truncate clip. Without it the
// suffix sits where the untruncated title would end and widens the inspector's
// scroll area, which lets the panel scroll sideways and cut off its left edge.
describe("inspector row labels", () => {
	const label = (
		<>
			PR #8821 a title long enough to be truncated in a narrow inspector
			<span className="sr-only">, open</span>
		</>
	);

	it("positions the plain row label so its sr-only suffix is clipped with it", () => {
		render(<InfoRow icon={GitPullRequest} label={label} />);
		const suffix = screen.getByText(", open");
		expect(suffix.parentElement).toHaveClass("truncate", "relative");
	});

	it("positions the button row label so its sr-only suffix is clipped with it", () => {
		render(<InfoRowLabelButton icon={GitPullRequest} label={label} onPress={() => {}} />);
		const suffix = screen.getByText(", open");
		expect(suffix.parentElement).toHaveClass("truncate", "relative");
	});
});
