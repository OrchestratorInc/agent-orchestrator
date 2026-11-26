import { requireNativeView } from "expo";
import type { ComponentType } from "react";
import { Platform, type ViewProps } from "react-native";
import type { DeviceStreamStats } from "../../lib/deviceQuality";

export type DeviceStreamState = "connecting" | "streaming" | "closed";

export type DeviceStreamViewProps = ViewProps & {
	/** "h264": the emulator's video over a WebSocket. "mjpeg": the simulator's multipart JPEG stream over HTTP. */
	mode: "h264" | "avcc" | "mjpeg";
	/** The daemon's route for the device's video, with its query. */
	url: string;
	/** Credentials for that socket; the phone holds them, the page never does. */
	headers: Record<string, string>;
	onState?: (event: { nativeEvent: { state: DeviceStreamState } }) => void;
	onSize?: (event: { nativeEvent: { width: number; height: number } }) => void;
	/** Every two seconds: how the link is carrying the stream (see useDeviceQuality). */
	onStats?: (event: { nativeEvent: DeviceStreamStats }) => void;
};

// A build that predates the native module (an OTA onto an old binary) has no
// view to draw: callers fall back to screenshots when this is null.
const registered = (Platform.OS === "android" || Platform.OS === "ios") && Boolean((globalThis as { expo?: { getViewConfig?: (name: string) => unknown } }).expo?.getViewConfig?.("AODeviceStream"));

/** The native view of a device's video (hardware H.264 or MJPEG), or null where the build lacks it. */
export const DeviceStreamView: ComponentType<DeviceStreamViewProps> | null = registered ? requireNativeView<DeviceStreamViewProps>("AODeviceStream") : null;
