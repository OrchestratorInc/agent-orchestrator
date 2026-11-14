import { Component } from "react";

type Props<T> = {
	liveKey: string | undefined;
	capture: (settledKey: string) => T | undefined;
	restore: (snapshot: T) => void;
};

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
