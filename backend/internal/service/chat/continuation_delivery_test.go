package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func continuationMessage(key string) ports.ChatUserMessage {
	return ports.ChatUserMessage{
		Text: "Continue from where you stopped.", ClientMessageID: key,
		Origin: domain.MessageOriginHuman, Continuation: true,
	}
}

func settleContinuationTestTurn(t *testing.T, h *harness, turn domain.ConversationTurn, partial string, state domain.TurnState) {
	t.Helper()
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID})
	if partial != "" {
		h.conv.emit(ports.ChatEvent{
			Kind: ports.ChatEventMessageDelta, ProviderTurnID: turn.ProviderTurnID,
			ProviderItemID: "answer-" + turn.ProviderTurnID, Delta: partial,
		})
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: state})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, current := range s.Turns {
			if current.ID == turn.ID {
				return current.State == state
			}
		}
		return false
	})
}

func seedContinuationTask(t *testing.T, h *harness) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "Write 30 AO tips without tools and end with TASK_DONE", ClientMessageID: "original-task",
	})
	if err != nil {
		t.Fatal(err)
	}
	settleContinuationTestTurn(t, h, turn, "1. Use worktrees.\n2. Keep the", domain.TurnStateInterrupted)
	return turn
}

func TestContinuationDeliveryPersistsEnrichedPromptAndIntakeIdentity(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	seedContinuationTask(t, h)
	request := continuationMessage("continue-1")
	turn, err := h.svc.Send(ctx, testSession, request)
	if err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentMessages()[1]
	if !strings.Contains(sent.Text, "TASK_DONE") || !strings.Contains(sent.Text, "Keep the") || !sent.Continuation {
		t.Fatalf("provider did not receive task context: %#v", sent)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 3 })
	if snapshot.Messages[2].Text != sent.Text || !snapshot.Messages[2].Continuation ||
		snapshot.Messages[2].ClientPayloadHash == "" {
		t.Fatalf("context and identity not persisted: %#v", snapshot.Messages[2])
	}
	settleContinuationTestTurn(t, h, turn, "TASK_DONE", domain.TurnStateCompleted)
	duplicate, err := h.svc.Send(ctx, testSession, request)
	if err != nil || duplicate.ID != "" || h.conv.sendCallCount() != 2 {
		t.Fatalf("duplicate after completion = %#v, %v, calls=%d", duplicate, err, h.conv.sendCallCount())
	}
	request.Text = "A different request reusing the same id"
	if _, err := h.svc.Send(ctx, testSession, request); !errors.Is(err, domain.ErrClientMessageConflict) {
		t.Fatalf("changed request should conflict: %v", err)
	}
}

func TestContinuationDeliveryRepeatedStopKeepsTaskAndNewestResponse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	seedContinuationTask(t, h)
	first, err := h.svc.Send(ctx, testSession, continuationMessage("continue-1"))
	if err != nil {
		t.Fatal(err)
	}
	settleContinuationTestTurn(t, h, first, "3. Give every session a", domain.TurnStateInterrupted)
	if _, err := h.svc.Send(ctx, testSession, continuationMessage("continue-2")); err != nil {
		t.Fatal(err)
	}
	text := h.conv.sentTexts()[2]
	if !strings.Contains(text, "TASK_DONE") || !strings.Contains(text, "Give every session a") ||
		strings.Count(text, "Interrupted task context (JSON):") != 1 {
		t.Fatalf("repeat continuation nested or lost context: %s", text)
	}
}

func TestContinuationDeliveryNewQuestionRemainsUnchanged(t *testing.T) {
	h := newHarness(t)
	seedContinuationTask(t, h)
	question := "Ignore that task and reply exactly NEW_QUESTION_OK"
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: question}); err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentTexts()[1]; got != question {
		t.Fatalf("ordinary message gained continuation context: %q", got)
	}
}

func TestContinuationDeliveryRetryUsesFrozenContextAndMarker(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	seedContinuationTask(t, h)
	continued, err := h.svc.Send(ctx, testSession, continuationMessage("continue-1"))
	if err != nil {
		t.Fatal(err)
	}
	frozen := h.conv.sentTexts()[1]
	settleContinuationTestTurn(t, h, continued, "A later partial response", domain.TurnStateFailed)
	retry, err := h.svc.RetryTurn(ctx, testSession, continued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentMessages()[2]; got.Text != frozen || !got.Continuation {
		t.Fatalf("retry reconstructed context or lost marker: %#v", got)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, message := range s.Messages {
			if message.TurnID == retry.ID {
				return message.Continuation
			}
		}
		return false
	})
	if len(snapshot.Turns) != 3 {
		t.Fatalf("retry changed historical turns: %#v", snapshot.Turns)
	}
}

func TestContinuationDeliveryQueuedPromptRetainsFrozenContext(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "Write 30 AO tips and end with QUEUED_TASK_DONE", ClientMessageID: "original-task",
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: first.ProviderTurnID},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: first.ProviderTurnID, ProviderItemID: "answer", Delta: "An unfinished tip"},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Second)
	// Model the interval after the durable stop commits but before the provider
	// completion event clears the controller's busy gate.
	if err := h.st.SettleTurnByID(ctx, first.ID, domain.TurnStateInterrupted, "", h.now()); err != nil {
		t.Fatal(err)
	}
	queued, err := h.svc.Send(ctx, testSession, continuationMessage("continue-queued"))
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("queue continuation: %#v, %v", queued, err)
	}
	durable, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || durable.TurnID != queued.ID {
		t.Fatalf("durable queue: %#v, %v", durable, err)
	}
	if !strings.Contains(durable.Text, "QUEUED_TASK_DONE") || !strings.Contains(durable.Text, "An unfinished tip") {
		t.Fatalf("queued continuation did not freeze context: %q", durable.Text)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: first.ProviderTurnID, TurnState: domain.TurnStateInterrupted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return h.conv.sendCallCount() == 2 })
	if got := h.conv.sentTexts()[1]; got != durable.Text {
		t.Fatalf("queue drain changed its accepted prompt: %q != %q", got, durable.Text)
	}
}

func TestContinuationDeliveryRetrySurvivesControllerRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	seedContinuationTask(t, h)
	continued, err := h.svc.Send(ctx, testSession, continuationMessage("continue-restart"))
	if err != nil {
		t.Fatal(err)
	}
	frozen := h.conv.sentTexts()[1]
	settleContinuationTestTurn(t, h, continued, "", domain.TurnStateFailed)
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}

	provider := newFakeConversation()
	provider.turnSeq = 10 // a reconnected provider's turn IDs are still unique
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
		NewID:   uuid.NewString, Now: h.now,
	})
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.RetryTurn(ctx, testSession, continued.ID); err != nil {
		t.Fatal(err)
	}
	sent := provider.sentMessages()
	if len(sent) != 1 || sent[0].Text != frozen || !sent[0].Continuation {
		t.Fatalf("restart retry lost its frozen context or marker: %#v", sent)
	}
}
