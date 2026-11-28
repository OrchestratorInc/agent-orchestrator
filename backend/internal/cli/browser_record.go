package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
)

const (
	maxBrowserRecordFPS        = 30
	maxBrowserRecordMaxSeconds = 1800
	// The desktop app returns a finished recording in chunks; this bounds the
	// loop for a recording at the app's size limit (500 MiB in 3 MiB pieces).
	maxBrowserRecordChunks = 1024
)

// A --size value: WIDTHxHEIGHT, or a device or desktop name the desktop app
// knows (iphone-12-pro, ipad-air, desktop, laptop, full-hd, ...).
var browserRecordSizePattern = regexp.MustCompile(`^(\d{2,4}x\d{2,4}|[a-z0-9][a-z0-9-]{0,40})$`)

// browserRecordFileResult is the JSON output of `ao browser record stop`.
type browserRecordFileResult struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	DurationMs int    `json:"durationMs"`
	Frames     int    `json:"frames"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	StopReason string `json:"stopReason,omitempty"`
}

func newBrowserRecordCommand(ctx *commandContext, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record the session's browser page to a WebM video",
		Long: "Record the session's browser page to a WebM video, from the page's own frames.\n" +
			"It records the active tab, follows tab switches, and keeps recording while the\n" +
			"Browser panel is hidden or the app is minimized. No screen recording permission\n" +
			"is involved. Start, drive the page with other `ao browser` commands, then stop\n" +
			"to save the video.",
		Args: noArgs,
	}

	var fps, maxSeconds int
	var size string
	var mobile bool
	start := &cobra.Command{
		Use:   "start",
		Short: "Start recording the active tab",
		Long: "Start recording the active tab. The page opens in its own window, sized to the\n" +
			"recording and fixed while it records, so the person can watch; the window closes\n" +
			"when the recording stops. A window already open is used instead and gets its own\n" +
			"size back afterwards. A size bigger than a window on the screen can show is\n" +
			"refused, with the largest size that fits.\n\n" +
			"--size lays the page out at that viewport for the whole recording, to show a\n" +
			"responsive layout (default desktop, 1280x800): WIDTHxHEIGHT (240 to 2560 each), a desktop size\n" +
			"(desktop 1280x800, laptop 1440x900, full-hd 1920x1080), or a device from the\n" +
			"Browser panel's device list, such as iphone-se, iphone-12-pro, pixel-8,\n" +
			"galaxy-s20-ultra, ipad-mini, ipad-air, or ipad-pro. Devices lay out as mobile\n" +
			"pages (their viewport meta tag applies); add --mobile for a WIDTHxHEIGHT that\n" +
			"should too. The page returns to its own size when the recording stops.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fps < 1 || fps > maxBrowserRecordFPS {
				return usageError{fmt.Errorf("--fps must be between 1 and %d", maxBrowserRecordFPS)}
			}
			if maxSeconds < 1 || maxSeconds > maxBrowserRecordMaxSeconds {
				return usageError{fmt.Errorf("--max-seconds must be between 1 and %d", maxBrowserRecordMaxSeconds)}
			}
			args := map[string]any{"fps": fps, "maxSeconds": maxSeconds}
			if size != "" {
				if !browserRecordSizePattern.MatchString(size) {
					return usageError{errors.New("--size must be WIDTHxHEIGHT or a device name such as iphone-12-pro")}
				}
				args["size"] = size
				args["mobile"] = mobile
			} else if mobile {
				return usageError{errors.New("--mobile requires --size")}
			}
			resp, err := ctx.browserAction(cmd.Context(), "record-start", args)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSON(cmd.OutOrStdout(), resp)
			}
			layout := ""
			if viewport, ok := resp.Result["viewport"].(map[string]any); ok {
				kind := "desktop"
				if isMobile, _ := viewport["mobile"].(bool); isMobile {
					kind = "mobile"
				}
				layout = fmt.Sprintf(" at %dx%d (%s)", numberInt(viewport["width"]), numberInt(viewport["height"]), kind)
			}
			where := "in its own window"
			switch resp.Result["window"] {
			case "none":
				where = "off screen (no window could open)"
			case "existing":
				where = "in its open window"
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Recording the browser page %s%s, up to %d fps; it stops by itself after %d s. Save it with `ao browser record stop [path]`.\n",
				where, layout, fps, maxSeconds,
			)
			return err
		},
	}
	start.Flags().IntVar(&fps, "fps", 10, fmt.Sprintf("most frames per second to record (maximum %d)", maxBrowserRecordFPS))
	start.Flags().IntVar(&maxSeconds, "max-seconds", 600, fmt.Sprintf("stop by itself after this many seconds (maximum %d)", maxBrowserRecordMaxSeconds))
	start.Flags().StringVar(&size, "size", "", "viewport to lay the page out at: WIDTHxHEIGHT, desktop, laptop, full-hd, or a device such as iphone-12-pro")
	start.Flags().BoolVar(&mobile, "mobile", false, "lay a WIDTHxHEIGHT --size out as a mobile page")
	cmd.AddCommand(start)

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether a recording is running",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := ctx.browserAction(cmd.Context(), "record-status", nil)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSON(cmd.OutOrStdout(), resp)
			}
			return writeBrowserRecordStatus(cmd, resp.Result)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop [path]",
		Short: "Stop recording and save the video",
		Long: "Stop recording and save the video as WebM (default ao-browser-<time>.webm in the\n" +
			"current directory). A recording that stopped by itself (its time or size limit, or\n" +
			"its session closing) is saved the same way.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "ao-browser-" + ctx.deps.Now().Format("20060102-150405.000") + ".webm"
			if len(args) == 1 {
				path = args[0]
			}
			resp, err := ctx.browserAction(cmd.Context(), "record-stop", nil)
			if err != nil {
				return err
			}
			saved, err := ctx.saveBrowserRecording(cmd.Context(), resp.Result, path)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return writeJSON(cmd.OutOrStdout(), saved)
			}
			note := ""
			switch saved.StopReason {
			case "max-duration":
				note = "; it had reached its time limit"
			case "max-size":
				note = "; it had reached its size limit"
			case "session-closed":
				note = "; it stopped when the browser session closed"
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Saved %s (%.1f s, %dx%d, %s)%s\n",
				saved.Path, float64(saved.DurationMs)/1000, saved.Width, saved.Height, formatRecordingSize(saved.Size), note,
			)
			return err
		},
	})
	return cmd
}

// saveBrowserRecording reads a finished recording back from the desktop app
// in chunks and writes it to target, which must not exist yet.
func (c *commandContext) saveBrowserRecording(ctx context.Context, result map[string]any, target string) (browserRecordFileResult, error) {
	id, _ := result["id"].(string)
	total := numberInt(result["bytes"])
	if id == "" || total <= 0 {
		return browserRecordFileResult{}, errors.New("browser returned no recording")
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return browserRecordFileResult{}, err
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return browserRecordFileResult{}, fmt.Errorf("refusing to overwrite existing recording %s", abs)
		}
		return browserRecordFileResult{}, err
	}
	written, err := c.copyBrowserRecording(ctx, file, id, total)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(abs)
		return browserRecordFileResult{}, err
	}
	reason, _ := result["stopReason"].(string)
	return browserRecordFileResult{
		Path:       abs,
		Size:       written,
		DurationMs: numberInt(result["durationMs"]),
		Frames:     numberInt(result["frames"]),
		Width:      numberInt(result["width"]),
		Height:     numberInt(result["height"]),
		StopReason: reason,
	}, nil
}

func (c *commandContext) copyBrowserRecording(ctx context.Context, file *os.File, id string, total int) (int64, error) {
	var written int64
	offset := 0
	for range maxBrowserRecordChunks {
		resp, err := c.browserAction(ctx, "record-read", map[string]any{"id": id, "offset": offset})
		if err != nil {
			return written, err
		}
		if numberInt(resp.Result["offset"]) != offset {
			return written, errors.New("browser returned a recording chunk out of order")
		}
		encoded, _ := resp.Result["data"].(string)
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return written, fmt.Errorf("decode browser recording: %w", err)
		}
		n, err := file.Write(data)
		written += int64(n)
		if err != nil {
			return written, err
		}
		next := numberInt(resp.Result["nextOffset"])
		if done, _ := resp.Result["done"].(bool); done {
			if next != total || written != int64(total) {
				return written, errors.New("browser recording ended early")
			}
			return written, nil
		}
		if next <= offset || next != offset+len(data) {
			return written, errors.New("browser returned a recording chunk out of order")
		}
		offset = next
	}
	return written, errors.New("browser recording is larger than expected")
}

func writeBrowserRecordStatus(cmd *cobra.Command, result map[string]any) error {
	if recording, _ := result["recording"].(bool); recording {
		_, err := fmt.Fprintf(
			cmd.OutOrStdout(),
			"Recording: %.1f s, %d frames at up to %d fps.\n",
			float64(numberInt(result["durationMs"]))/1000, numberInt(result["frames"]), numberInt(result["fps"]),
		)
		return err
	}
	if finished, ok := result["finished"].(map[string]any); ok {
		_, err := fmt.Fprintf(
			cmd.OutOrStdout(),
			"Not recording. A finished recording (%.1f s) waits: save it with `ao browser record stop [path]`.\n",
			float64(numberInt(finished["durationMs"]))/1000,
		)
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), "Not recording.")
	return err
}

// formatRecordingSize renders a saved recording's size, as KB under a megabyte.
func formatRecordingSize(size int64) string {
	if size < 1<<20 {
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
}
