import { useEffect, useState } from "react";
import { aoBridge } from "../lib/bridge";
import { isMacPlatform } from "../lib/platform";

export function useWindowFullScreen(): boolean {
	const [fullScreen, setFullScreen] = useState(false);
	useEffect(() => {
		if (!isMacPlatform()) return;

		let live = true;
		let version = 0;

		const off = aoBridge.window.onFullScreen((value) => {
			version += 1;
			setFullScreen(value);
		});

		const seedVersion = version;
		void aoBridge.window.isFullScreen().then((value) => {
			if (live && seedVersion === version) setFullScreen(value);
		});

		return () => {
			live = false;
			off?.();
		};
	}, []);
	return fullScreen;
}
