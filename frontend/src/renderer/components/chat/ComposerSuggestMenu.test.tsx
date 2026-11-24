import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";

describe("ComposerSuggestMenu", () => {
	it("marks each file row with its file type's icon, like the Files panel", () => {
		render(
			<ComposerSuggestMenu
				id="mention"
				kind="file"
				highlighted={0}
				onPick={() => {}}
				items={[
					{ value: "src/Button.tsx", label: "Button.tsx", detail: "src" },
					{ value: "src/index.ts", label: "index.ts", detail: "src" },
				]}
			/>,
		);
		const [tsx, ts] = screen.getAllByRole("option").map((row) => row.querySelector("svg"));
		expect(tsx).not.toBeNull();
		expect(ts).not.toBeNull();
		// The old generic icons drew .tsx and .ts the same; the file-type set does not.
		expect(tsx!.innerHTML).not.toBe(ts!.innerHTML);
	});

	it("shows no file icon in the skill menu", () => {
		render(
			<ComposerSuggestMenu
				id="slash"
				kind="skill"
				highlighted={1}
				onPick={() => {}}
				items={[{ value: "review", label: "review" }, { value: "ship", label: "ship" }]}
			/>,
		);
		expect(screen.getAllByRole("option")[0].querySelector("svg")).toBeNull();
	});
});
