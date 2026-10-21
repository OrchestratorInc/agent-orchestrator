import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { SearchablePicker } from "./SearchablePicker";

it("filters a long repository list and selects the matching private repository", async () => {
	const user = userEvent.setup();
	function Example() {
		const [value, setValue] = useState("");
		return <SearchablePicker
			ariaLabel="Repository"
			placeholder="Select a repository"
			searchPlaceholder="Search repositories"
			value={value}
			onChange={setValue}
			options={[
				{ value: "one", label: "acme/public" },
				{ value: "two", label: "acme/private", private: true },
				...Array.from({ length: 10 }, (_, index) => ({ value: `filler-${index}`, label: `acme/filler-${index}` })),
			]}
		/>;
	}
	render(<Example />);

	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	await user.type(screen.getByPlaceholderText("Search repositories"), "private");
	expect(screen.queryByRole("option", { name: "acme/public" })).not.toBeInTheDocument();
	const option = screen.getByRole("option", { name: "acme/private" });
	expect(option.querySelector("svg.lucide-lock")).not.toBeNull();
	await user.click(option);
	expect(screen.getByRole("combobox", { name: "Repository" })).toHaveTextContent("acme/private");
});

it("keeps a large result list scrollable below the search field without empty space for short lists", async () => {
	const user = userEvent.setup();
	render(<SearchablePicker
		ariaLabel="Template"
		placeholder="Default"
		searchPlaceholder="Search templates"
		value=""
		onChange={() => undefined}
		options={Array.from({ length: 40 }, (_, index) => ({ value: String(index), label: `Template ${index}` }))}
	/>);
	await user.click(screen.getByRole("combobox", { name: "Template" }));
	const list = screen.getByRole("listbox", { name: "Template" });
	expect(list).toHaveClass("overflow-y-auto");
	expect(list).toHaveClass("max-h-72");
	expect(screen.getByPlaceholderText("Search templates")).toBeInTheDocument();
	expect(screen.getAllByRole("option")).toHaveLength(40);
});

it("gives repository selectors a visibly scrollable result area that fits short lists", async () => {
	const user = userEvent.setup();
	render(<SearchablePicker
		ariaLabel="Repository"
		placeholder="Select a repository"
		searchPlaceholder="Search repositories"
		fixedScroll
		value=""
		onChange={() => undefined}
		options={Array.from({ length: 40 }, (_, index) => ({ value: String(index), label: `repo-${index}` }))}
	/>);
	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	const list = screen.getByRole("listbox", { name: "Repository" });
	// Capped, not fixed: one repository renders as one row, not an 18rem box.
	expect(list).toHaveClass("max-h-72", "overflow-y-auto", "repository-picker-scrollbar");
	expect(list).not.toHaveClass("h-72");
	expect(screen.getAllByRole("option")).toHaveLength(40);
});

// The popover is portaled to <body>, so inside a modal Dialog react-remove-scroll
// preventDefault()s the native wheel scroll. The picker drives the scroll itself
// from a non-passive wheel listener, which must survive that preventDefault.
it("scrolls an overflowing result list from the wheel and cancels the native scroll", async () => {
	const user = userEvent.setup();
	render(<SearchablePicker
		ariaLabel="Repository"
		placeholder="Select a repository"
		searchPlaceholder="Search repositories"
		fixedScroll
		value=""
		onChange={() => undefined}
		options={Array.from({ length: 40 }, (_, index) => ({ value: String(index), label: `repo-${index}` }))}
	/>);
	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	const list = screen.getByRole("listbox", { name: "Repository" });

	// jsdom does no layout, so drive scrollTop/overflow through explicit descriptors.
	let scrollTop = 0;
	Object.defineProperty(list, "scrollTop", { configurable: true, get: () => scrollTop, set: (value) => { scrollTop = value; } });
	Object.defineProperty(list, "scrollHeight", { configurable: true, value: 1000 });
	Object.defineProperty(list, "clientHeight", { configurable: true, value: 288 });

	const wheel = new WheelEvent("wheel", { deltaY: 120, bubbles: true, cancelable: true });
	list.dispatchEvent(wheel);
	expect(scrollTop).toBe(120);
	expect(wheel.defaultPrevented).toBe(true);
});

it("leaves a non-overflowing result list to scroll natively", async () => {
	const user = userEvent.setup();
	render(<SearchablePicker
		ariaLabel="Repository"
		placeholder="Select a repository"
		searchPlaceholder="Search repositories"
		fixedScroll
		value=""
		onChange={() => undefined}
		options={[{ value: "one", label: "repo-one" }]}
	/>);
	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	const list = screen.getByRole("listbox", { name: "Repository" });

	let scrollTop = 0;
	Object.defineProperty(list, "scrollTop", { configurable: true, get: () => scrollTop, set: (value) => { scrollTop = value; } });
	Object.defineProperty(list, "scrollHeight", { configurable: true, value: 100 });
	Object.defineProperty(list, "clientHeight", { configurable: true, value: 288 });

	const wheel = new WheelEvent("wheel", { deltaY: 120, bubbles: true, cancelable: true });
	list.dispatchEvent(wheel);
	expect(scrollTop).toBe(0);
	expect(wheel.defaultPrevented).toBe(false);
});

it("offers search only when the list has more than ten options", async () => {
	const user = userEvent.setup();
	const options = (count: number) => Array.from({ length: count }, (_, index) => ({ value: String(index), label: `repo-${index}` }));
	const { rerender } = render(<SearchablePicker ariaLabel="Repository" placeholder="Select a repository" searchPlaceholder="Search repositories" value="" onChange={() => undefined} options={options(10)} />);
	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	expect(screen.queryByPlaceholderText("Search repositories")).not.toBeInTheDocument();
	expect(screen.getAllByRole("option")).toHaveLength(10);

	rerender(<SearchablePicker ariaLabel="Repository" placeholder="Select a repository" searchPlaceholder="Search repositories" value="" onChange={() => undefined} options={options(11)} />);
	expect(screen.getByPlaceholderText("Search repositories")).toBeInTheDocument();
});

it("runs a pinned action below the options and closes the list", async () => {
	const user = userEvent.setup();
	const onSelect = vi.fn();
	render(<SearchablePicker ariaLabel="Repository" placeholder="Select a repository" searchPlaceholder="Search repositories" value="" onChange={() => undefined}
		options={[{ value: "one", label: "acme/one" }]}
		action={{ label: "Connect more repositories", onSelect }} />);
	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	await user.click(screen.getByRole("button", { name: "Connect more repositories" }));
	expect(onSelect).toHaveBeenCalledTimes(1);
	expect(screen.queryByRole("listbox", { name: "Repository" })).not.toBeInTheDocument();
});

it("can open on mount and reports a dismissal without a choice, but not a choice", async () => {
	const user = userEvent.setup();
	const onDismiss = vi.fn();
	const onChange = vi.fn();
	const props = {
		ariaLabel: "Repository",
		placeholder: "Select a repository",
		searchPlaceholder: "Search repositories",
		value: "",
		onChange,
		onDismiss,
		options: [{ value: "one", label: "acme/one" }],
	};
	const { unmount } = render(<SearchablePicker {...props} defaultOpen />);
	expect(screen.getByRole("listbox", { name: "Repository" })).toBeInTheDocument();
	await user.click(screen.getByRole("option", { name: "acme/one" }));
	expect(onChange).toHaveBeenCalledWith("one");
	expect(onDismiss).not.toHaveBeenCalled();
	unmount();

	render(<SearchablePicker {...props} defaultOpen />);
	await user.keyboard("{Escape}");
	expect(onDismiss).toHaveBeenCalledOnce();
	expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
});

it("does not open on mount while disabled", () => {
	render(<SearchablePicker ariaLabel="Repository" placeholder="Select" searchPlaceholder="Search" value="" onChange={() => undefined} options={[]} defaultOpen disabled />);
	expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
});

it("says the list is loading from a status already in the page when it opens", async () => {
	const user = userEvent.setup();
	render(<SearchablePicker
		ariaLabel="Repository"
		placeholder="Select a repository"
		searchPlaceholder="Search repositories"
		value=""
		onChange={vi.fn()}
		options={[]}
		loading
		loadingText="Loading repositories..."
	/>);
	const status = screen.getByRole("status");
	expect(status).toBeEmptyDOMElement();
	await user.click(screen.getByRole("combobox", { name: "Repository" }));
	expect(screen.getByRole("status")).toBe(status);
	expect(status).toHaveTextContent("Loading repositories...");
	// The visible line inside the list is not a second live region.
	expect(screen.getByRole("listbox", { name: "Repository" }).querySelector("[role=status]")).toBeNull();
});
