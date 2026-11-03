package session

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Unchanged gaps occupy one segment regardless of how many rows the viewer expands.
type capturedDiffSegment struct {
	old, new, length, index int
	kind                    byte
	text                    string
}

var capturedDiffHunk = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func capturedRevisionLines(revision WorkspaceFileRevision) []string {
	if !revision.Exists || revision.Content == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(revision.Content, "\r\n", "\n"), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Complete Git patches describe every change, but omit unchanged gaps. Reconstruct
// only their coordinates, not their row objects or another full-file diff.
func capturedDiffSegments(patch string, oldCount, newCount int) ([]capturedDiffSegment, bool) {
	var segments []capturedDiffSegment
	oldNo, newNo, index := 1, 1, 0
	oldRemaining, newRemaining := 0, 0
	inHunk := false
	files := 0
	gap := func(oldEnd, newEnd int) bool {
		length := oldEnd - oldNo
		if length < 0 || newEnd-newNo != length || oldEnd > oldCount+1 || newEnd > newCount+1 {
			return false
		}
		if length > 0 {
			segments = append(segments, capturedDiffSegment{old: oldNo, new: newNo, length: length, index: index, kind: '='})
			index += length
		}
		oldNo, newNo = oldEnd, newEnd
		return true
	}
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			files++
			if files > 1 || inHunk {
				return nil, false
			}
			continue
		}
		if header := capturedDiffHunk.FindStringSubmatch(line); header != nil {
			if oldRemaining != 0 || newRemaining != 0 {
				return nil, false
			}
			oldStart, e1 := strconv.Atoi(header[1])
			newStart, e2 := strconv.Atoi(header[3])
			oldRemaining, newRemaining = 1, 1
			var e3, e4 error
			if header[2] != "" {
				oldRemaining, e3 = strconv.Atoi(header[2])
			}
			if header[4] != "" {
				newRemaining, e4 = strconv.Atoi(header[4])
			}
			if (oldRemaining == 0 && newRemaining == 0) || e1 != nil || e2 != nil || e3 != nil || e4 != nil {
				return nil, false
			}
			if oldRemaining == 0 {
				oldStart++
			}
			if newRemaining == 0 {
				newStart++
			}
			if oldStart < 1 || newStart < 1 || !gap(oldStart, newStart) {
				return nil, false
			}
			inHunk = true
			continue
		}
		if line == "" {
			continue
		}
		if line == `\ No newline at end of file` {
			if !inHunk {
				return nil, false
			}
			continue
		}
		if !inHunk {
			metadata := false
			for _, prefix := range []string{"index ", "--- ", "+++ ", "old mode ", "new mode ", "new file mode ", "deleted file mode ", "similarity index ", "dissimilarity index ", "rename from ", "rename to ", "copy from ", "copy to "} {
				metadata = metadata || strings.HasPrefix(line, prefix)
			}
			if !metadata {
				return nil, false
			}
			continue
		}
		kind := line[0]
		if kind != ' ' && kind != '+' && kind != '-' {
			return nil, false
		}
		segment := capturedDiffSegment{length: 1, index: index, kind: kind, text: line[1:]}
		if kind != '+' {
			if oldRemaining <= 0 || oldNo > oldCount {
				return nil, false
			}
			segment.old = oldNo
			oldNo++
			oldRemaining--
		}
		if kind != '-' {
			if newRemaining <= 0 || newNo > newCount {
				return nil, false
			}
			segment.new = newNo
			newNo++
			newRemaining--
		}
		segments = append(segments, segment)
		index++
	}
	if oldRemaining != 0 || newRemaining != 0 || !gap(oldCount+1, newCount+1) {
		return nil, false
	}
	return segments, true
}

func resolveCapturedDiffInterval(ref ports.ChatFileReference, result FileReferenceResolution, patch string, before, after WorkspaceFileRevision) (FileReferenceResolution, bool) {
	if before.Binary || after.Binary || before.Truncated || after.Truncated || len(ref.Lines) == 0 {
		return result, false
	}
	oldLines, newLines := capturedRevisionLines(before), capturedRevisionLines(after)
	segments, ok := capturedDiffSegments(patch, len(oldLines), len(newLines))
	if !ok {
		return result, false
	}
	if len(segments) == 1 && segments[0].kind == '=' && !slices.Equal(oldLines, newLines) {
		return result, false
	}
	bySide := [2][]int{}
	for i, segment := range segments {
		if segment.old > 0 {
			bySide[0] = append(bySide[0], i)
		}
		if segment.new > 0 {
			bySide[1] = append(bySide[1], i)
		}
	}
	oldSide := func(row ports.ChatFileReferenceLine) bool {
		return row.Side == "old" || (row.Side != "new" && (row.Kind == "del" || (ref.Side == "old" && row.Kind != "add")))
	}
	offsets := [2]int{result.OldLineOffset, result.NewLineOffset}
	seen := [2]bool{}
	for _, row := range ref.Lines {
		if oldSide(row) {
			seen[0] = true
		} else {
			seen[1] = true
		}
	}
	previous := -1
	firstLine, lastLine := 0, 0
	for i, row := range ref.Lines {
		side := 1
		if oldSide(row) {
			side = 0
		}
		number := row.NewNo
		if side == 0 {
			number = row.OldNo
		}
		if number == nil {
			return result, false
		}
		position := *number + offsets[side]
		rows := bySide[side]
		start := func(segment capturedDiffSegment) int {
			if side == 0 {
				return segment.old
			}
			return segment.new
		}
		at := sort.Search(len(rows), func(j int) bool { return start(segments[rows[j]])+segments[rows[j]].length > position })
		if at == len(rows) || start(segments[rows[at]]) > position {
			return result, false
		}
		segment := segments[rows[at]]
		delta := position - start(segment)
		logical := segment.index + delta
		if i > 0 && logical != previous+1 {
			return result, false
		}
		previous = logical
		oldNo, newNo := segment.old, segment.new
		if oldNo > 0 {
			oldNo += delta
		}
		if newNo > 0 {
			newNo += delta
		}
		context := segment.kind == '=' || segment.kind == ' '
		if (context && row.Kind != "context") || (segment.kind == '+' && row.Kind != "add") || (segment.kind == '-' && row.Kind != "del") {
			return result, false
		}
		if segment.kind != '=' && segment.text != row.Text {
			return result, false
		}
		if context {
			if row.OldNo == nil || row.NewNo == nil || oldLines[oldNo-1] != row.Text || newLines[newNo-1] != row.Text {
				return result, false
			}
		} else if (side == 0 && oldLines[oldNo-1] != row.Text) || (side == 1 && newLines[newNo-1] != row.Text) {
			return result, false
		}
		for side, number := range [2]*int{row.OldNo, row.NewNo} {
			actual := [2]int{oldNo, newNo}[side]
			if number == nil || actual == 0 {
				continue
			}
			offset := actual - *number
			if seen[side] && offsets[side] != offset {
				return result, false
			}
			offsets[side], seen[side] = offset, true
		}
		if i == 0 {
			firstLine = position
		}
		lastLine = position
	}
	result.Line, result.EndLine = firstLine, lastLine
	result.OldLineOffset, result.NewLineOffset = offsets[0], offsets[1]
	if offsets[0] != 0 || offsets[1] != 0 {
		result.State = "moved"
	}
	return result, true
}
