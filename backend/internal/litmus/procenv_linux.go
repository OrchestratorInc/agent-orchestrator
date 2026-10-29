package litmus

import (
	"os"
	"strconv"
	"strings"
)

func processEnviron(pid int) ([]string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}
