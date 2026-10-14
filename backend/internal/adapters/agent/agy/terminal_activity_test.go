package agy

import (
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInspectTerminalSurface(t *testing.T) {
	p := New()

	tests := []struct {
		name           string
		output         string
		wantWork       ports.TerminalSurfaceWorkState
		wantComposer   ports.TerminalComposerState
		wantNotStarted bool
		wantDetectIdle bool
	}{
		{
			name: "initial idle screen with banner",
			output: `      ▄▀▀▄        Antigravity CLI 1.3.4
     ▀▀▀▀▀▀       test@example.com (Google AI Pro)
    ▀▀▀▀▀▀▀▀      Gemini 3.8 Flash (Medium)
   ▄▀▀    ▀▀▄     /workspace
  ▄▀▀      ▀▀▄

 ┃ Verbosity now defaults to medium
 ┃ esc to dismiss
──────────────────────────────────────────────────────────
>
──────────────────────────────────────────────────────────
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkIdle,
			wantComposer:   ports.TerminalComposerEmpty,
			wantNotStarted: true,
			wantDetectIdle: true,
		},
		{
			name: "idle screen with user typed draft without prompt prefix",
			output: `──────────────────────────────────────────────────────────
How do I refactor this service?
──────────────────────────────────────────────────────────
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkIdle,
			wantComposer:   ports.TerminalComposerDraft,
			wantNotStarted: false,
			wantDetectIdle: false,
		},
		{
			name: "idle screen with user typed draft with prompt prefix",
			output: `──────────────────────────────────────────────────────────
> How do I refactor this service?
──────────────────────────────────────────────────────────
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkIdle,
			wantComposer:   ports.TerminalComposerDraft,
			wantNotStarted: false,
			wantDetectIdle: false,
		},
		{
			name: "idle screen after turn with assistant question in transcript",
			output: `> hi

  Hello! How can I help you today with your project?

──────────────────────────────────────────────────────────
>
──────────────────────────────────────────────────────────
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkIdle,
			wantComposer:   ports.TerminalComposerEmpty,
			wantNotStarted: false,
			wantDetectIdle: true,
		},
		{
			name: "waiting input permission prompt",
			output: `Tool requested to execute shell command: rm -rf ./tmp
Allow / Deny:
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkWaitingInput,
			wantComposer:   ports.TerminalComposerUnknown,
			wantNotStarted: false,
			wantDetectIdle: false,
		},
		{
			name: "active work with spinner",
			output: `⠋ Thinking about your request...
──────────────────────────────────────────────────────────
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkActive,
			wantComposer:   ports.TerminalComposerUnknown,
			wantNotStarted: false,
			wantDetectIdle: false,
		},
		{
			name: "active work with status text",
			output: `Working...
──────────────────────────────────────────────────────────
? for shortcuts                                  Gemini 3.8`,
			wantWork:       ports.TerminalSurfaceWorkActive,
			wantComposer:   ports.TerminalComposerUnknown,
			wantNotStarted: false,
			wantDetectIdle: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.InspectTerminalSurface(tt.output)
			if got.Work != tt.wantWork {
				t.Errorf("InspectTerminalSurface Work = %v, want %v", got.Work, tt.wantWork)
			}
			if got.Composer != tt.wantComposer {
				t.Errorf("InspectTerminalSurface Composer = %v, want %v", got.Composer, tt.wantComposer)
			}
			if got.NativeConversationNotStarted != tt.wantNotStarted {
				t.Errorf("InspectTerminalSurface NativeConversationNotStarted = %v, want %v", got.NativeConversationNotStarted, tt.wantNotStarted)
			}

			state, auth := p.DetectTerminalActivity(tt.output)
			if tt.wantDetectIdle {
				if !auth || state != domain.ActivityIdle {
					t.Errorf("DetectTerminalActivity = (%v, %v), want (idle, true)", state, auth)
				}
			} else {
				if auth && state == domain.ActivityIdle {
					t.Errorf("DetectTerminalActivity unexpectedly reported idle")
				}
			}
		})
	}
}

func TestLiveCapturedOutput(t *testing.T) {
	data, err := os.ReadFile("/tmp/live_output.txt")
	if err != nil {
		t.Skip("no live output")
	}
	p := New()
	obs := p.InspectTerminalSurface(string(data))
	t.Logf("obs: Work=%v, Composer=%v, NotStarted=%v", obs.Work, obs.Composer, obs.NativeConversationNotStarted)
	if obs.Work != ports.TerminalSurfaceWorkIdle {
		t.Errorf("expected TerminalSurfaceWorkIdle, got %v", obs.Work)
	}
	if obs.Composer != ports.TerminalComposerEmpty {
		t.Errorf("expected TerminalComposerEmpty, got %v", obs.Composer)
	}
	if obs.NativeConversationNotStarted {
		t.Errorf("expected NativeConversationNotStarted=false for ongoing conversation")
	}
}
