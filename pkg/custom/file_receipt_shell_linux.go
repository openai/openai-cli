package custom

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

func fileReceiptGoCallerName(pid int) (string, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// comm can contain spaces and parentheses. Fields after its closing ')' start
	// with process state, then the parent PID.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return "", errors.New("Go launcher process record is unavailable")
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 2 {
		return "", errors.New("Go launcher parent is unavailable")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil || parent <= 1 || parent == pid {
		return "", errors.New("Go launcher parent is unavailable")
	}
	return os.Readlink("/proc/" + strconv.Itoa(parent) + "/exe")
}
