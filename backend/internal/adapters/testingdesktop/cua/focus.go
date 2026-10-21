package cua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Cua's long-lived NSWorkspace snapshot can disagree with its WindowServer
// front-process result. Observe activation in a fresh AppKit process whose run
// loop services workspace notifications. This only waits; it never activates an
// app, raises a window or sends input. Both app identity and exact key window
// must agree before the bounded wait returns success.
const focusedWindowScript = `ObjC.import("AppKit"); ObjC.import("ApplicationServices");
function run(argv) {
 var pid=Number(argv[0]),wid=Number(argv[1]),answer={trusted:Boolean($.AXIsProcessTrusted())};
 if(!answer.trusted){answer.error="Accessibility permission is unavailable";return JSON.stringify(answer);}
 ObjC.bindFunction("_AXUIElementGetWindow", ["int", ["void *", "unsigned int *"]]);
 var deadline=Date.now()+2000;
 function observe(){
  answer.workspacePID=null;answer.focusedPID=null;answer.windowID=null;
  var front=$.NSWorkspace.sharedWorkspace.frontmostApplication;
  if(front)answer.workspacePID=Number(front.processIdentifier);
  var app=Ref(),appPID=Ref(),window=Ref(),windowID=Ref();
  if($.AXUIElementCopyAttributeValue($.AXUIElementCreateSystemWide(),$("AXFocusedApplication"),app)!==0)return;
  if($.AXUIElementGetPid(app[0],appPID)!==0)return;
  answer.focusedPID=Number(appPID[0]);
  if(answer.focusedPID!==pid)return;
  if($.AXUIElementCopyAttributeValue(app[0],$("AXFocusedWindow"),window)!==0)return;
  if($._AXUIElementGetWindow(window[0],windowID)!==0)return;
  answer.windowID=Number(windowID[0]);
 }
 while(true){
  observe();
  if(answer.workspacePID===pid && answer.focusedPID===pid && answer.windowID===wid)return JSON.stringify(answer);
  if(Date.now()>=deadline){answer.timedOut=true;return JSON.stringify(answer);}
  $.NSRunLoop.currentRunLoop.runUntilDate($.NSDate.dateWithTimeIntervalSinceNow(0.02));
 }
}`

func (a *Adapter) waitFocusedWindow(ctx context.Context, b *binding) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, runErr := a.run(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", focusedWindowScript, strconv.Itoa(b.target.ElectronPID), b.target.WindowID)
	var observed struct {
		Trusted      bool   `json:"trusted"`
		WorkspacePID *int   `json:"workspacePID"`
		FocusedPID   *int   `json:"focusedPID"`
		WindowID     *int   `json:"windowID"`
		TimedOut     bool   `json:"timedOut"`
		Error        string `json:"error"`
	}
	decodeErr := json.Unmarshal(out.Stdout, &observed)
	if runErr != nil || decodeErr != nil || ctx.Err() != nil {
		return &Error{Code: "window_not_focused", Detail: providerCallDiagnostic(ctx, "native foreground readback", a.targetArgs(b), "cannot observe exact foreground window", runErr, decodeErr), cause: errors.Join(ErrRefused, runErr, decodeErr, ctx.Err())}
	}
	if !observed.Trusted || observed.Error != "" || observed.TimedOut ||
		observed.WorkspacePID == nil || *observed.WorkspacePID != b.target.ElectronPID ||
		observed.FocusedPID == nil || *observed.FocusedPID != b.target.ElectronPID ||
		observed.WindowID == nil || strconv.Itoa(*observed.WindowID) != b.target.WindowID {
		return refuse("window_not_focused", fmt.Sprintf("native foreground did not settle on owned PID %d key window %s within 2s: %s", b.target.ElectronPID, b.target.WindowID, providerDiagnosticText(string(out.Stdout), nil, providerDiagnosticLimit)))
	}
	return nil
}
