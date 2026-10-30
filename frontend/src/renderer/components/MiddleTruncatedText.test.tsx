import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MiddleTruncatedText } from "./MiddleTruncatedText";

describe("MiddleTruncatedText", () => {
	it("shows the shortened value but gives screen readers the full one", () => {
		const { container } = render(<MiddleTruncatedText text="v0.0.1792022479-ge66e24" format={(version) => `${version} available`} />);
		expect(container.querySelector("[aria-hidden='true']")).toHaveTextContent("v0.0.179…6e24 available");
		expect(container.querySelector(".sr-only")).toHaveTextContent("v0.0.1792022479-ge66e24 available");
		expect(screen.getByTitle("v0.0.1792022479-ge66e24")).toBeInTheDocument();
	});

	it("renders a value that already fits once", () => {
		const { container } = render(<MiddleTruncatedText text="v2.1.300" />);
		expect(container).toHaveTextContent(/^v2\.1\.300$/);
		expect(container.querySelector(".sr-only")).toBeNull();
	});
});
