package scheduler

import "strings"

// WaitWords says in words why a job waits (scheduling R5.48, web-console R5.20).
// text is the gate's own line with its numbers ("memory 91% > 85%", "2 ahead in
// the queue"); it wins when it is not empty, because it names the figures. With
// no text the reason alone gives a plain sentence. The words are the ones in
// docs/design-system/next-version/content.md.
func WaitWords(reason WaitReason, text string) string {
	text = strings.TrimSpace(text)
	switch reason {
	case WaitLoadNotMeasured:
		return "Load not measured yet. The machine cannot read its load, so the job waits. Run the doctor to see why."
	case WaitLoadOverBudget:
		if text != "" {
			return "Over the load budget: " + text + "."
		}
		return "Over the memory or CPU budget."
	case WaitReserved:
		if text != "" {
			return "Reserved: " + text + "."
		}
		return "Reserved. The machine keeps room free for this job."
	case WaitFreeSlot, WaitLostRace:
		if text != "" {
			return "No free slot: " + text + "."
		}
		return "No free slot."
	case WaitOverMemory:
		if text != "" {
			return "Stopped over memory. " + text
		}
		return "Stopped over memory."
	case WaitNoRunnerLabels:
		return "No machine has this OS and these labels."
	case WaitMachineDrained:
		return "The machine is drained."
	case WaitRepoParallelLimit:
		if text != "" {
			return "The repository is at its parallel limit: " + text + "."
		}
		return "The repository is at its parallel limit."
	default:
		return "Waiting. The reason is not known yet."
	}
}
