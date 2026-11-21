import { render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { useAncestorFlag, useDocumentFlag } from "./useElementFlag";

function DocumentFlag({ active }: { active: boolean }) {
	useDocumentFlag("data-test-flag", active);
	return null;
}

function AncestorFlag({ active }: { active: boolean }) {
	const ref = useRef<HTMLSpanElement>(null);
	useAncestorFlag(ref, ".scope", "data-test-flag", active);
	return <span ref={ref} />;
}

afterEach(() => document.documentElement.removeAttribute("data-test-flag"));

describe("useDocumentFlag", () => {
	it("keeps the flag while any active caller is mounted", () => {
		const first = render(<DocumentFlag active />);
		const second = render(<DocumentFlag active />);
		expect(document.documentElement.hasAttribute("data-test-flag")).toBe(true);

		first.unmount();
		expect(document.documentElement.hasAttribute("data-test-flag")).toBe(true);

		second.unmount();
		expect(document.documentElement.hasAttribute("data-test-flag")).toBe(false);
	});

	it("follows the active state", () => {
		const view = render(<DocumentFlag active={false} />);
		expect(document.documentElement.hasAttribute("data-test-flag")).toBe(false);
		view.rerender(<DocumentFlag active />);
		expect(document.documentElement.hasAttribute("data-test-flag")).toBe(true);
		view.rerender(<DocumentFlag active={false} />);
		expect(document.documentElement.hasAttribute("data-test-flag")).toBe(false);
	});
});

describe("useAncestorFlag", () => {
	it("flags only the closest matching ancestor while active", () => {
		const view = render(
			<div className="scope" data-testid="outer">
				<div className="scope" data-testid="inner">
					<AncestorFlag active />
				</div>
			</div>,
		);
		expect(view.getByTestId("inner").hasAttribute("data-test-flag")).toBe(true);
		expect(view.getByTestId("outer").hasAttribute("data-test-flag")).toBe(false);

		view.rerender(
			<div className="scope" data-testid="outer">
				<div className="scope" data-testid="inner">
					<AncestorFlag active={false} />
				</div>
			</div>,
		);
		expect(view.getByTestId("inner").hasAttribute("data-test-flag")).toBe(false);
	});
});
