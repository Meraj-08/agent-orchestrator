package agy

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var agySpinners = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// DetectTerminalActivity reports idle only when Antigravity's composer and footer are visible.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	observation := p.InspectTerminalSurface(output)
	if observation.Work == ports.TerminalSurfaceWorkIdle && observation.Composer != ports.TerminalComposerDraft {
		return domain.ActivityIdle, true
	}
	return "", false
}

// InspectTerminalSurface reports Antigravity work and composer facts separately.
func (p *Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	lines := terminalui.PlainTerminalLines(output)
	if len(lines) < 2 {
		return ports.TerminalSurfaceObservation{}
	}

	observation := ports.TerminalSurfaceObservation{}

	// 1. Permission / input prompts fail closed as waiting input.
	if agyWaitingInput(lines) {
		observation.Work = ports.TerminalSurfaceWorkWaitingInput
		return observation
	}

	// 2. Active work (spinner or progress state).
	if agyActiveWork(lines) {
		observation.Work = ports.TerminalSurfaceWorkActive
		return observation
	}

	// 3. Settled idle prompt with "? for shortcuts".
	footerIdx := agyFindFooter(lines)
	if footerIdx >= 0 {
		observation.Work = ports.TerminalSurfaceWorkIdle
		observation.Composer = agyComposerState(output, lines, footerIdx)
		if agyInitialBanner(lines) && observation.Composer == ports.TerminalComposerEmpty {
			observation.NativeConversationNotStarted = true
		}
		return observation
	}

	return observation
}

func agyWaitingInput(lines []string) bool {
	start := len(lines) - 8
	if start < 0 {
		start = 0
	}
	// If a bordered composer exists below, older text is transcript, not a pending decision.
	hasBorderedComposer := false
	for i := len(lines) - 1; i >= start; i-- {
		if isDivider(strings.TrimSpace(lines[i])) {
			for j := i - 1; j >= 0; j-- {
				if isDivider(strings.TrimSpace(lines[j])) {
					hasBorderedComposer = true
					break
				}
			}
			break
		}
	}
	if hasBorderedComposer {
		return false
	}

	for i := start; i < len(lines); i++ {
		lower := strings.ToLower(lines[i])
		if strings.Contains(lower, "allow / deny") ||
			strings.Contains(lower, "[y/n]") ||
			strings.Contains(lower, "(y/n)") ||
			strings.Contains(lower, "approve?") ||
			strings.Contains(lower, "confirm?") ||
			strings.Contains(lower, "do you want to proceed") {
			return true
		}
	}
	return false
}

func agyActiveWork(lines []string) bool {
	start := len(lines) - 8
	if start < 0 {
		start = 0
	}
	for i := start; i < len(lines); i++ {
		line := lines[i]
		for _, s := range agySpinners {
			if strings.Contains(line, s) {
				return true
			}
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "thinking...") ||
			strings.Contains(lower, "working...") ||
			strings.Contains(lower, "generating...") {
			return true
		}
	}
	return false
}

func agyFindFooter(lines []string) int {
	start := len(lines) - 10
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		line := strings.ToLower(lines[i])
		if strings.Contains(line, "? for shortcuts") || strings.Contains(line, "for shortcuts") {
			return i
		}
	}
	return -1
}

func agyComposerState(output string, lines []string, footerIdx int) ports.TerminalComposerState {
	for _, marker := range []string{">", "›", "❯"} {
		state := terminalui.LastBorderedPromptComposerState(output, marker)
		switch state {
		case terminalui.ComposerEmpty:
			return ports.TerminalComposerEmpty
		case terminalui.ComposerDraft:
			return ports.TerminalComposerDraft
		}
	}

	// Fallback: parse lines bounded between composer dividers above the footer.
	lowerDivider := -1
	for i := footerIdx - 1; i >= 0; i-- {
		if isDivider(strings.TrimSpace(lines[i])) {
			lowerDivider = i
			break
		}
	}

	upperDivider := -1
	if lowerDivider >= 0 {
		for i := lowerDivider - 1; i >= 0; i-- {
			if isDivider(strings.TrimSpace(lines[i])) {
				upperDivider = i
				break
			}
		}
	}

	start := 0
	end := footerIdx
	if upperDivider >= 0 && lowerDivider > upperDivider {
		start = upperDivider + 1
		end = lowerDivider
	} else if lowerDivider >= 0 {
		start = lowerDivider - 5
		if start < 0 {
			start = 0
		}
		end = lowerDivider
	} else {
		start = footerIdx - 5
		if start < 0 {
			start = 0
		}
		end = footerIdx
	}

	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || isDivider(trimmed) || isAgyBannerChrome(trimmed) {
			continue
		}
		content := trimmed
		for _, marker := range []string{">", "›", "❯"} {
			if strings.HasPrefix(content, marker) {
				content = strings.TrimSpace(strings.TrimPrefix(content, marker))
				break
			}
		}
		if content != "" {
			return ports.TerminalComposerDraft
		}
	}
	return ports.TerminalComposerEmpty
}

func isDivider(s string) bool {
	if len(s) < 16 {
		return false
	}
	for _, r := range s {
		if r != '─' && r != '-' && r != '━' && r != '=' {
			return false
		}
	}
	return true
}

func isAgyBannerChrome(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "esc to dismiss") ||
		strings.Contains(lower, "verbosity now defaults") ||
		strings.Contains(lower, "verbosity is now") ||
		strings.Contains(lower, "antigravity cli") ||
		strings.Contains(lower, "gemini")
}

func agyInitialBanner(lines []string) bool {
	hasBanner := false
	for _, line := range lines {
		if strings.Contains(line, "Antigravity CLI") {
			hasBanner = true
			break
		}
	}
	if !hasBanner {
		return false
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, m := range []string{">", "❯", "›"} {
			if strings.HasPrefix(trimmed, m) {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, m))
				if rest != "" {
					return false
				}
			}
		}
		if strings.HasPrefix(trimmed, "└ Tip:") ||
			strings.Contains(trimmed, "Generating...") ||
			strings.Contains(trimmed, "Thinking...") {
			return false
		}
	}
	return true
}
