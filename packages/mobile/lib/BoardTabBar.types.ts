import type { BoardTabName } from "./board-tabs";

export type BoardTabBarProps = {
	/** The tab on screen. */
	selected: BoardTabName;
	onSelect: (tab: BoardTabName) => void;
	/** Workers waiting on a person, shown on the Workers tab; 0 shows none. */
	waiting: number;
	onNewTask: () => void;
};
