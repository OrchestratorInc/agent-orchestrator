import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { TableFileView } from "./TableFileView";

const column = (index: number) => Array.from(screen.getByRole("table").querySelectorAll("tbody tr")).map((row) => row.children[index]?.textContent);

describe("TableFileView", () => {
	it("sorts a numeric column by value across negatives, thousands separators, decimals and empty cells", async () => {
		render(<TableFileView content={"name,delta\na,1.10\nb,-5\nc,\nd,\"1,000\"\ne,1.5\nf,999\ng,5\n"} path="deltas.csv" />);
		const sortButton = screen.getByRole("button", { name: "delta" });

		await userEvent.click(sortButton);
		expect(column(1)).toEqual(["-5", "1.10", "1.5", "5", "999", "1,000", ""]);
		await userEvent.click(sortButton);
		expect(column(1)).toEqual(["1,000", "999", "5", "1.5", "1.10", "-5", ""]);
	});

	it("does not read a comma decimal as a thousands-separated number", async () => {
		render(<TableFileView content={"name,ratio\na,\"1,5\"\nb,2\nc,\"1,25\"\n"} path="ratios.csv" />);
		expect(screen.getByRole("columnheader", { name: "ratio" })).not.toHaveClass("text-right");
		await userEvent.click(screen.getByRole("button", { name: "ratio" }));
		// Sorted as text, so `1,5` is not treated as 15 and placed after `2`.
		expect(column(1).indexOf("1,5")).toBeLessThan(column(1).indexOf("2"));
	});

	it("sorts a text column with natural ordering", async () => {
		render(<TableFileView content={"file\nitem10\nItem2\nitem1\n"} path="files.csv" />);
		await userEvent.click(screen.getByRole("button", { name: "file" }));
		expect(column(0)).toEqual(["item1", "Item2", "item10"]);
	});

	it("keeps empty cells last in a text column in both directions", async () => {
		render(<TableFileView content={"owner,id\nbeta,1\n,2\nalpha,3\n"} path="owners.csv" />);
		const sortButton = screen.getByRole("button", { name: "owner" });
		await userEvent.click(sortButton);
		expect(column(0)).toEqual(["alpha", "beta", ""]);
		await userEvent.click(sortButton);
		expect(column(0)).toEqual(["beta", "alpha", ""]);
	});
});
