import { Component } from "react";

type Props<T> = {
	/** Changes whenever rows above the view may have been added or dropped. */
	historyKey: string;
	/** Reads the page as it was just before the commit that changes `historyKey`. */
	capture: () => T | undefined;
	/** Runs after that commit, before paint, with what `capture` returned. */
	restore: (snapshot: T) => void;
};

/**
 * Hands the chat the layout from just before the rows above the view change (an
 * older page loading, a reload re-paging the history), and again just after,
 * inside the same commit. Function components have no hook that runs before
 * React changes the DOM, so this is a class for getSnapshotBeforeUpdate.
 */
export class HistoryAnchorSnapshot<T> extends Component<Props<T>> {
	getSnapshotBeforeUpdate(previous: Readonly<Props<T>>): T | null {
		if (previous.historyKey === this.props.historyKey) return null;
		return this.props.capture() ?? null;
	}

	componentDidUpdate(_previous: Readonly<Props<T>>, _state: unknown, snapshot: T | null) {
		if (snapshot) this.props.restore(snapshot);
	}

	render() {
		return null;
	}
}
