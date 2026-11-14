import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
	composeMenuCloseAutoFocus,
	type MenuTeardownDetail,
	onMenuTeardownComplete,
	useMenuReturnTarget,
} from "./menu-focus";

const MENU_ID = "menu-content";

function OpenMenu() {
	const contentRef = useMenuReturnTarget<HTMLDivElement>();
	return (
		<>
			<button type="button" aria-controls={MENU_ID} aria-expanded>
				Project actions
			</button>
			<div id={MENU_ID} role="menu" ref={contentRef}>
				<button type="button">New session</button>
			</div>
		</>
	);
}

function ContextMenuLike() {
	const contentRef = useMenuReturnTarget<HTMLDivElement>();
	return (
		<div role="menu" ref={contentRef} tabIndex={-1}>
			<button type="button">New session</button>
		</div>
	);
}

async function openContextMenu() {
	const openedFrom = document.createElement("button");
	document.body.append(openedFrom);
	openedFrom.focus();

	const { getByRole } = render(<ContextMenuLike />);
	const menu = getByRole("menu");
	menu.focus();
	await Promise.resolve();
	return { menu, openedFrom };
}

async function openMenu() {
	const { getByRole } = render(<OpenMenu />);
	await Promise.resolve();
	return {
		menu: getByRole("menu"),
		item: getByRole("button", { name: "New session" }),
		trigger: getByRole("button", { name: "Project actions" }),
	};
}

function closeMenu(menu: HTMLElement) {
	const event = new CustomEvent("focusScope.autoFocusOnUnmount", { cancelable: true });
	menu.addEventListener("focusScope.autoFocusOnUnmount", composeMenuCloseAutoFocus() as EventListener, {
		once: true,
	});
	menu.dispatchEvent(event);
	return event;
}

function openDialogField() {
	const field = document.createElement("textarea");
	document.body.append(field);
	field.focus();
	return field;
}

function closeDialogField(field: HTMLElement) {
	field.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
	field.remove();
}

afterEach(() => {
	vi.useRealTimers();
});

describe("keepFocusOnOpenedSurface", () => {
	it("lets Radix return focus to the trigger when nothing else claimed it", async () => {
		const { menu } = await openMenu();
		expect(closeMenu(menu).defaultPrevented).toBe(false);
	});

	it("lets Radix return focus to the trigger while focus is still in the menu", async () => {
		const { menu, item } = await openMenu();
		item.focus();
		expect(closeMenu(menu).defaultPrevented).toBe(false);
	});

	it("keeps focus where a dialog opened by the selected item put it", async () => {
		const { menu } = await openMenu();
		const field = openDialogField();

		expect(closeMenu(menu).defaultPrevented).toBe(true);
		expect(document.activeElement).toBe(field);
	});

	it("hands focus back to the trigger once that dialog closes", async () => {
		const { menu, trigger } = await openMenu();
		const field = openDialogField();
		closeMenu(menu);

		closeDialogField(field);

		await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
	});

	it("abandons the hand-back once focus finds a home outside the dialog", async () => {
		const { menu, trigger } = await openMenu();
		const field = openDialogField();
		closeMenu(menu);

		const elsewhere = document.createElement("input");
		document.body.append(elsewhere);
		field.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: elsewhere }));
		elsewhere.focus();
		field.remove();

		const unrelated = document.createElement("button");
		document.body.append(unrelated);
		unrelated.focus();
		unrelated.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
		unrelated.remove();

		await new Promise((resolve) => setTimeout(resolve, 60));
		expect(document.activeElement).not.toBe(trigger);
	});

	it("keeps waiting while the surface that borrowed the caret is still open", async () => {
		const { menu, trigger } = await openMenu();
		const field = openDialogField();
		closeMenu(menu);

		field.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
		(document.activeElement as HTMLElement | null)?.blur();
		await new Promise((resolve) => setTimeout(resolve, 60));
		expect(document.activeElement).not.toBe(trigger);

		closeDialogField(field);
		await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
	});

	it("returns a context menu to where it was opened from, not to the menu itself", async () => {
		const { menu, openedFrom } = await openContextMenu();
		const field = openDialogField();
		closeMenu(menu);

		closeDialogField(field);

		await vi.waitFor(() => expect(document.activeElement).toBe(openedFrom));
		openedFrom.remove();
	});

	it("leaves focus alone if something else claimed it before the hand-back", async () => {
		const { menu, trigger } = await openMenu();
		const field = openDialogField();
		closeMenu(menu);

		closeDialogField(field);
		const elsewhere = document.createElement("input");
		document.body.append(elsewhere);
		elsewhere.focus();

		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(document.activeElement).toBe(elsewhere);
		expect(document.activeElement).not.toBe(trigger);
	});
});

describe("menu teardown signal", () => {
	it("reports the menu and its trigger once the restore has been decided", async () => {
		const { menu, trigger } = await openMenu();
		const field = openDialogField();
		const seen: MenuTeardownDetail[] = [];
		const unsubscribe = onMenuTeardownComplete((detail) => seen.push(detail));

		const event = closeMenu(menu);

		expect(event.defaultPrevented).toBe(true);
		expect(seen).toHaveLength(1);
		expect(seen[0].menu).toBe(menu);
		expect(seen[0].trigger).toBe(trigger);
		unsubscribe();
		field.remove();
	});

	it("stops reporting once the listener unsubscribes", async () => {
		const { menu } = await openMenu();
		const field = openDialogField();
		const seen: MenuTeardownDetail[] = [];
		const unsubscribe = onMenuTeardownComplete((detail) => seen.push(detail));

		closeMenu(menu);
		unsubscribe();
		closeMenu(menu);

		expect(seen).toHaveLength(1);
		field.remove();
	});

	it("reports even when nothing borrowed the caret", async () => {
		const { menu } = await openMenu();
		const seen: MenuTeardownDetail[] = [];
		const unsubscribe = onMenuTeardownComplete((detail) => seen.push(detail));

		expect(closeMenu(menu).defaultPrevented).toBe(false);

		expect(seen).toHaveLength(1);
		expect(seen[0].menu).toBe(menu);
		unsubscribe();
	});
});
