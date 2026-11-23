# ao device

Open an iOS Simulator or Android Emulator for this AO session, put the app you
built on it, and drive it. The user watches and taps the same device in the
desktop Device tab and in the AO phone app.

Device support is opt-in. If `ao device open` answers `DEVICES_DISABLED`, ask
the user to turn it on in the Device tab or to run `ao device setup`; do not
run setup yourself unless the user asked for device support.

## Loop

1. Open a device. Without flags AO picks the platform from the project and
   this machine, and explains its pick:

   ```bash
   ao device open
   ao device open --platform android
   ao device open --device "iPhone 17 Pro"
   ```

   Reopening returns this session's existing device. Other sessions' devices
   are never handed out.

2. Build with the project's own tools and fix build errors yourself, aimed at
   this session's device (the `Device id:` line `ao device open` printed, or
   `lease.device.id` with `--json`): for example
   `npx expo run:ios --device <udid>`, `xcodebuild -destination id=<udid>`, or
   `npx react-native run-android --deviceId <serial>`. Other sessions' devices
   may be running too, and Android tools without a device install on every one
   of them: `./gradlew installDebug` does, so build the APK
   (`./gradlew assembleDebug`) and install it with `ao device install`, or set
   `ANDROID_SERIAL=<serial>` for the Gradle run. When a build only produces an
   artifact, install it:

   ```bash
   ao device install ios/build/Build/Products/Debug-iphonesimulator/App.app
   ao device launch com.example.app
   ```

3. Drive and verify with the agent-device command that `ao device open`
   printed. It is already bound to this session's device, so concurrent
   sessions never collide: run it with a subcommand and nothing else. Use the
   exact path it printed; a login shell may not have it on `PATH`.

   ```bash
   <agent-device> open com.example.app
   <agent-device> snapshot -i
   <agent-device> click @e3
   <agent-device> fill @e5 "hello"
   <agent-device> screenshot /tmp/device.png
   ```

   `snapshot` and taps need an agent-device session on the running app, so run
   `open <app-id>` first; it attaches to an app `ao device launch` already
   started. Refs go stale after every tap: take a fresh `snapshot -i` before
   the next one.

   Prefer snapshot refs over coordinates. Run `<agent-device> help workflow`
   for waits, recovery and platform limits. Do every tap, text entry, alert
   and screenshot through it so the user can watch. `xcrun simctl` and `adb`
   stay fine for builds, installs, logs and port forwards, such as
   `adb reverse tcp:8081 tcp:8081` for Metro on Android.

4. Release the device when you are done. `--shutdown` also powers it off:

   ```bash
   ao device close
   ```

   A session that ends releases its device automatically within half a
   minute; the device keeps running for the next session.

## Other commands

- `ao device status` lists platform availability, simulators, emulators and
  which session holds each device.
- `ao device setup` turns device support on: it installs the pinned device
  tools under the AO data directory and starts the device hub.
- Every command accepts `--json`.

If `ao device open` answers `NO_DEVICE_AVAILABLE` or `NO_FREE_DEVICE`, report
its message to the user: it says what is missing (Xcode, the Android SDK, a
free device) and what they can do.
