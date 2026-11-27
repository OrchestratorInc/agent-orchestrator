import { create } from "zustand";

export type ReadyConnector = { key: string; name: string };

export const useConnectorRestartStore = create<{
	ready: Record<string, ReadyConnector>;
	offer: (session: string, connector?: ReadyConnector) => void;
}>((set) => ({
	ready: {},
	offer: (session, connector) =>
		set((state) => {
			const { [session]: _settled, ...rest } = state.ready;
			return { ready: connector ? { ...rest, [session]: connector } : rest };
		}),
}));
