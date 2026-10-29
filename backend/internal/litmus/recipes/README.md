# Recipe isolation

`isolation.home: true` gives each leg a private HOME for $HOME-based state. Electron recipes default to true; other kinds default to false. Litmus copies no owner provider state and does not check provider login state. A private HOME does not cover Cocoa-resolved ~/Library paths on macOS. Set the application's profile location in the recipe when it uses those paths.

`isolation.env` supplies launch-only environment values. Values may expand `$LITMUS_ROOT`, `$LITMUS_LEG`, `$LITMUS_LEG_DIR`, `$LITMUS_RUN_ID`, `$LITMUS_RUN_NUM`, `$LITMUS_APP_PORT`, `$LITMUS_DEBUG_PORT` and `$LITMUS_HOME`. `$LITMUS_HOME` names the private home directory and requires `home: true`. Litmus owns HOME, XDG paths, npm paths and app-data paths when private HOME is enabled, so those environment keys cannot be set in `isolation.env`.

`isolation.dirs` is an optional list of expanded directories. Every directory must be strictly inside `$LITMUS_LEG_DIR`. Litmus creates them before launch and removes them during cleanup. Paths that escape through symlinks fail at launch. Example:

```yaml
isolation:
  home: true
  dirs: ["$LITMUS_LEG_DIR/profile"]
  env:
    APP_PROFILE: "$LITMUS_LEG_DIR/profile"
    PROVIDER_HOME: "$LITMUS_HOME/.provider"
```

Every leg gets a temporary directory outside the run directory. On macOS it is a short path under `/private/tmp` to leave room for Unix socket names. Litmus records this path and the recipe directories in `owned.json` for teardown, failed launches and stale cleanup. `isolation.env` configures the application; it does not decide which processes cleanup stops.
