// Package gitbundle reads a git bundle's header. A bundle names the commits
// the receiving repository must already hold (its prerequisites) before it
// can be fetched from; pkg/forkseed and pkg/filesnapshot report them, so a
// caller can make sure the receiver has them first.
package gitbundle

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNotBundle means the file does not start with a git bundle header.
var ErrNotBundle = errors.New("not a git bundle")

// Prerequisites are the commits the bundle at path requires, in header order.
// A self-contained bundle has none.
func Prerequisites(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	signature, err := reader.ReadString('\n')
	if err != nil || (signature != "# v2 git bundle\n" && signature != "# v3 git bundle\n") {
		return nil, fmt.Errorf("%w: %s", ErrNotBundle, path)
	}
	var prerequisites []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read bundle header: %w", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			// The header ends with an empty line; the pack follows.
			return prerequisites, nil
		case strings.HasPrefix(line, "-"):
			id, _, _ := strings.Cut(line[1:], " ")
			prerequisites = append(prerequisites, id)
		}
	}
}
