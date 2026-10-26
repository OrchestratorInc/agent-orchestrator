/**
 * The board's tabs, in tab-bar order.
 *
 * `name` is the tab's route under app/(tabs). Each tab is a route group holding
 * a stack of its own, which is what gives its page a native header, while the
 * group keeps the URL each page always had.
 */
export const boardTabs = [
	{ name: "(workers)", title: "Workers", href: "/" },
	{ name: "(projects)", title: "Projects", href: "/projects" },
	{ name: "(prs)", title: "Pull Requests", href: "/prs" },
] as const;

export type BoardTab = (typeof boardTabs)[number];
export type BoardTabName = BoardTab["name"];

/**
 * The board's name while `tab` shows, `tab` being the tab navigator's route
 * name. Undefined means the navigator has not picked one yet, so its first tab,
 * Workers, is the one on screen.
 */
export function boardTitle(tab: string | undefined): string {
	return (boardTabs.find(({ name }) => name === tab) ?? boardTabs[0]).title;
}
