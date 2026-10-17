# Codex reconnect completion recovery

Native Electron captures use an isolated checkout and scratch AO data. The real Codex 0.162.1 app-server and its persistent host survive the daemon interruption. All prompts and replies are synthetic; no database rows were hand-edited.

| Before | After |
| --- | --- |
| Native turn finished; AO still shows Working and the follow-up remains queued. | Recovery restores the completed turn and the same provider processes the follow-up. |

## Failure sequence

1. Start a Codex Chat worker whose command waits on a scratch gate file.
2. Confirm AO has bound the native running turn and command.
3. Suspend only the isolated daemon, release the gate, and observe `task_complete` for that exact native turn in the Codex rollout.
4. Kill the suspended daemon before it can commit the completion.
5. Click Restart daemon in the native dev window, keeping the original host and Codex process alive.
6. Send `Reply exactly AFTER_RECONNECT_OK` and compare the durable turn, queue, and controller state.

This injects the completion-loss window during daemon replacement; it does not simulate downloading or installing an updater artifact.

## Evidence

- [Before screenshot](before.png) and [measured native/daemon timeline](before.json).
- [After screenshot](after.png) and [same-session recovery assertions](after.json).
- [Fresh repetition with the fixed daemon](repeat-fixed.json): the same interruption recovers the original turn and a new message completes on the surviving provider.
- [Original-code regression failure](regression-before.txt) and [fixed-code regression pass](regression-after.txt).

The before daemon was built from `edf0bfaacbf63f3df693dcb88f72e9a1d7168480`; the same Go regression also fails with the PR base `3242c14a6b`’s original Codex adapter, loaded through a Go source overlay. The before screenshot uses this branch’s renderer with the original daemon; it shows the unchanged busy/queue behavior. Captures are direct macOS captures of the verified Electron lab window.

The after capture is the exact same `standalone-10` session and original turn as the before capture. Its previously queued message completes with `AFTER_RECONNECT_OK`; the original host and Codex PID/start-time identities are unchanged. The recovered answer appears exactly once, its command is completed, and the controller is ready. A subsequent message typed and sent through the native Electron composer receives `UI_SEND_OK`; `after.json` records this additional check.

## Automated regression

```sh
cd backend
go test ./internal/adapters/chatdriver/codexappserver ./internal/service/chat -run 'TestLiveReconnect|TestLiveTurnHistory' -count=1 -v
```

The service regression uses the actual adapter, service, and SQLite store. It retains a running turn and queued follow-up, reconnects to an initialized surviving provider, and supplies native completion without replaying its lost notification.
