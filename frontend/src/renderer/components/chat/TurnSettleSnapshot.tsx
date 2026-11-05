import { Component } from "react";

type Props<T> = {
	/** The running turn's group key; undefined once nothing is running. */
	liveKey: string | undefined;
	/** Reads the page as it was just before the commit that settles `settledKey`. */
	capture: (settledKey: string) => T | undefined;
	/** Runs after that commit, before paint, with what `capture` returned. */
	restore: (snapshot: T) => void;
};

/**
 * Hands the chat the layout from just before a live turn settles, and again just
 * after, inside the same commit. Settling removes the very steps whose positions
 * the scroll fix needs, and function components have no hook that runs before
 * React changes the DOM, so this is a class for getSnapshotBeforeUpdate.
 */
export class TurnSettleSnapshot<T> extends Component<Props<T>> {
	getSnapshotBeforeUpdate(previous: Readonly<Props<T>>): T | null {
		if (!previous.liveKey || previous.liveKey === this.props.liveKey) return null;
		return this.props.capture(previous.liveKey) ?? null;
	}

	componentDidUpdate(_previous: Readonly<Props<T>>, _state: unknown, snapshot: T | null) {
		if (snapshot) this.props.restore(snapshot);
	}

	render() {
		return null;
	}
}
