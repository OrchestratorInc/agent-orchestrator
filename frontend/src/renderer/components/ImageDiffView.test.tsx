import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ImageDiffView } from "./ImageDiffView";

vi.mock("../lib/api-client", () => ({
	getApiBaseUrl: () => "",
}));

describe("ImageDiffView", () => {
	it("clears a side's load failure once the image version changes", async () => {
		const { rerender } = render(
			<ImageDiffView path="docs/logo.png" sessionId="sess-1" split status="modified" version={1} />,
		);

		fireEvent.error(await screen.findByAltText("Before version of docs/logo.png"));
		expect(await screen.findByText("Image preview could not be loaded.")).toBeInTheDocument();

		rerender(<ImageDiffView path="docs/logo.png" sessionId="sess-1" split status="modified" version={2} />);

		const before = await screen.findByAltText("Before version of docs/logo.png");
		expect(before).toHaveAttribute("crossorigin", "anonymous");
		expect(before).toHaveAttribute("src", expect.stringContaining("side=before&v=2"));
		expect(screen.queryByText("Image preview could not be loaded.")).not.toBeInTheDocument();
	});
});
