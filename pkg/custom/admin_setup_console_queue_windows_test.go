//go:build windows

package custom

import "golang.org/x/sys/windows"

const adminConsoleQueuedRecordCount = 5000

// Queue metadata never includes record contents or handle values.
type adminConsoleQueueEvidence struct {
	RecordsRequested     uint32 `json:"records_requested"`
	RecordsWritten       uint32 `json:"records_written"`
	PendingBefore        uint32 `json:"pending_before"`
	PendingAfter         uint32 `json:"pending_after"`
	WriteSucceeded       bool   `json:"write_succeeded"`
	CountBeforeSucceeded bool   `json:"count_before_succeeded"`
	CountAfterSucceeded  bool   `json:"count_after_succeeded"`
	EchoOffBefore        bool   `json:"echo_off_before"`
	EchoOffAfter         bool   `json:"echo_off_after"`
	Passed               bool   `json:"passed"`
}

func adminConsoleInjectQueuedRecords() *adminConsoleQueueEvidence {
	evidence := &adminConsoleQueueEvidence{RecordsRequested: adminConsoleQueuedRecordCount}
	input, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return evidence
	}
	var mode uint32
	evidence.EchoOffBefore = windows.GetConsoleMode(input, &mode) == nil && mode&windows.ENABLE_ECHO_INPUT == 0
	if !evidence.EchoOffBefore {
		return evidence
	}
	records := make([]adminSetupConsoleInputRecord, adminConsoleQueuedRecordCount)
	defer clear(records)
	for i := range records {
		// Alternating characters prevent adjacent repeat coalescing.
		records[i] = adminSetupConsoleInputRecord{eventType: 1, keyDown: 1, repeat: 1, character: uint16('Q' + i%2)}
	}
	evidence.RecordsWritten, err = adminConsoleWriteNativeRecords(records)
	evidence.WriteSucceeded = err == nil && evidence.RecordsWritten == evidence.RecordsRequested
	evidence.CountBeforeSucceeded = windows.GetNumberOfConsoleInputEvents(input, &evidence.PendingBefore) == nil
	return evidence
}

func (evidence *adminConsoleQueueEvidence) complete(cleanupSucceeded bool) {
	input, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	evidence.EchoOffAfter = windows.GetConsoleMode(input, &mode) == nil && mode&windows.ENABLE_ECHO_INPUT == 0
	evidence.CountAfterSucceeded = windows.GetNumberOfConsoleInputEvents(input, &evidence.PendingAfter) == nil
	evidence.Passed = cleanupSucceeded && evidence.WriteSucceeded && evidence.CountBeforeSucceeded && evidence.PendingBefore > 32 &&
		evidence.CountAfterSucceeded && evidence.PendingAfter == 0 && evidence.EchoOffBefore && evidence.EchoOffAfter
}
