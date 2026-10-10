import { requireOptionalNativeModule, NativeModule } from "expo";
import { Platform } from "react-native";

type WatchRequest = { id: string; payload: string; deadline: number };
declare class WatchBridge extends NativeModule<{ onWatchRequest: (event: WatchRequest) => void }> {
	publishSnapshot(json: string): Promise<void>;
	setReady(ready: boolean): Promise<void>;
	isRequestActive(id: string): Promise<boolean>;
	completeRequest(id: string, json: string): Promise<void>;
}
// Older phone binaries and Android remain usable; new native code requires a rebuild.
export const watchBridge = Platform.OS === "ios" ? requireOptionalNativeModule<WatchBridge>("AOWatchBridge") : null;
