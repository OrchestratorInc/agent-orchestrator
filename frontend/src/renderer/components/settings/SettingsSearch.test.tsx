import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SettingsSearch } from "./SettingsSearch";

describe("SettingsSearch", () => {
	it("reports typed queries and renders the trailing action", () => {
		const onChange = vi.fn();
		render(
			<SettingsSearch
				aria-label="Search downloads"
				onChange={onChange}
				placeholder="Search downloads"
				trailing={<button type="button">Clear all</button>}
				value=""
			/>,
		);
		fireEvent.change(screen.getByRole("searchbox", { name: "Search downloads" }), { target: { value: "pdf" } });
		expect(onChange).toHaveBeenCalledWith("pdf");
		expect(screen.getByRole("button", { name: "Clear all" })).toBeInTheDocument();
	});
});
