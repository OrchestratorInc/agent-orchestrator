package hostupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	resultFile     = "host-update.json"
	appAction      = "__app-update"
	appSession     = domain.SessionID("__app-update")
	appUpdateLimit = 25 * time.Minute
	linkClosed     = "BROWSER_COMMAND_CANCELED"
)

// AppControl is the desktop app's command link. The action used here is
// internal: it is not in the browser allowlist, so `ao browser` cannot send it.
type AppControl interface {
	Execute(ctx context.Context, sessionID domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error)
}

type appResult struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (u *Updater) loadAppResult() {
	data, err := os.ReadFile(u.resultPath)
	var result appResult
	if err != nil || json.Unmarshal(data, &result) != nil || result.To == "" {
		return
	}
	if result.From != u.version {
		_ = os.Remove(u.resultPath)
		return
	}
	u.notApplied = result.To
	u.appErr = fmt.Sprintf("The update to %s didn't apply on that computer. Update Orchestrator there.", result.To)
}

func (u *Updater) startApp() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.appRunning {
		return ErrInProgress
	}
	u.appRunning, u.appErr = true, ""
	go u.runApp(u.notApplied)
	return nil
}

func (u *Updater) runApp(notApplied string) {
	ctx, cancel := context.WithTimeout(context.Background(), appUpdateLimit)
	defer cancel()
	result, err := u.app.Execute(ctx, appSession, appAction, map[string]interface{}{"step": "prepare", "notApplied": notApplied})
	reply, _ := result.Value.(map[string]interface{})
	version, _ := reply["version"].(string)
	if current, _ := reply["current"].(bool); err == nil && current {
		u.finishApp("")
		return
	}
	if err != nil || version == "" {
		u.finishApp(appError(err))
		return
	}
	data, _ := json.Marshal(appResult{From: u.version, To: version})
	if err := os.WriteFile(u.resultPath, data, 0o600); err != nil {
		u.finishApp(err.Error())
		return
	}
	_, err = u.app.Execute(ctx, appSession, appAction, map[string]interface{}{"step": "install", "version": version})
	var refused browserruntime.CommandError
	if errors.Is(err, browserruntime.ErrReconnecting) || (errors.As(err, &refused) && refused.Code != linkClosed) {
		_ = os.Remove(u.resultPath)
		u.finishApp(appError(err))
		return
	}
	time.Sleep(u.restartWait)
	u.finishApp(fmt.Sprintf("Orchestrator on that computer didn't restart to install %s.", version))
}

func (u *Updater) finishApp(message string) {
	u.mu.Lock()
	u.appRunning, u.appErr = false, message
	u.mu.Unlock()
}

func appError(err error) string {
	var refused browserruntime.CommandError
	switch {
	case errors.As(err, &refused):
		return refused.Message
	case errors.Is(err, browserruntime.ErrUnavailable):
		return "Orchestrator's window is closed on that computer. Open it there, then try again."
	case errors.Is(err, context.DeadlineExceeded):
		return "The update took longer than 25 minutes."
	case err != nil:
		return err.Error()
	}
	return "Orchestrator on that computer found no update to install."
}
