import { useUiStore, type UiState } from "../stores/ui-store";

/**
 * Devices are a Developer mode preview. Off, sessions have no Device tab, an
 * agent's `ao device open` does not reveal one, and the daemon refuses device
 * work (the shell sends Developer mode to PUT /devices/availability). Remove
 * this flag once devices ship to everyone.
 */
export function devicesEnabled(state: Pick<UiState, "developerMode">): boolean {
	return state.developerMode;
}

export function useDevicesEnabled(): boolean {
	return useUiStore(devicesEnabled);
}
