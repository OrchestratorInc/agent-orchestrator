import { requireOptionalNativeModule, type NativeModule } from "expo";
import { Platform } from "react-native";

type WatchBridge = NativeModule & { publishSnapshot(json: string): Promise<void> };
// Older phone binaries and Android remain usable; new native code requires a rebuild.
export const watchBridge = Platform.OS === "ios" ? requireOptionalNativeModule<WatchBridge>("AOWatchBridge") : null;
