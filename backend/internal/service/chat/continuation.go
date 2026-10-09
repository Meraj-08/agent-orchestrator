package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const continuationTextBudget = 8 * 1024

// prepareContinuation runs under sendMu, before persisting the provider request.
// The intake hash remains the client's original payload. Persisting the enriched
// text makes queue recovery and failed-turn retries send the same context rather
// than reconstructing it from a conversation that may have changed meanwhile.
func (c *Controller) prepareContinuation(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	return prepareContinuation(ctx, c.store, c.continuationReader, c.conversation.ID, msg)
}

func prepareContinuation(ctx context.Context, store Store, reader SnapshotReader, conversationID string, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	if !msg.Continuation || reader == nil {
		return msg, nil
	}
	if msg.ClientMessageID != "" {
		_, found, err := store.ConversationMessageByClientID(ctx, conversationID, msg.ClientMessageID)
		if err != nil {
			return msg, fmt.Errorf("read continuation delivery: %w", err)
		}
		if found {
			// AppendUserMessage performs the fingerprint/conflict check. A duplicate
			// must not depend on whether the task is still the latest stopped turn.
			return msg, nil
		}
	}
	rows, err := reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return msg, fmt.Errorf("read interrupted conversation: %w", err)
	}
	reminder := stoppedContinuationContext(rows)
	if reminder != "" {
		msg.Text += "\n\n" + reminder
	}
	return msg, nil
}

// stoppedContinuationContext consumes the active-lineage snapshot, never another
// session's history. It supplies a task reminder, not a replacement transcript or
// an execution checkpoint. Reasoning and tool outputs are deliberately omitted.
func stoppedContinuationContext(rows ConversationRows) string {
	turns := make(map[string]domain.ConversationTurn, len(rows.Turns))
	var stopped domain.ConversationTurn
	for _, turn := range rows.Turns {
		if turn.RolledBackAt != nil || turn.State == domain.TurnStateCancelled {
			continue
		}
		// Stop also interrupts queued messages that never reached the provider.
		// Those are not the task the user wants to continue.
		if turn.State == domain.TurnStateInterrupted && turn.ProviderTurnID == "" {
			continue
		}
		turns[turn.ID] = turn
		stopped = turn
	}
	if stopped.State != domain.TurnStateInterrupted {
		return ""
	}

	var original domain.ConversationMessage
	var cutoff int64
	for _, message := range rows.Messages {
		if message.TurnID == stopped.ID && message.Role == domain.MessageRoleUser {
			cutoff = message.Sequence
			break
		}
	}
	if cutoff == 0 {
		return ""
	}
	for _, message := range rows.Messages {
		turn, visible := turns[message.TurnID]
		if !visible || turn.State == domain.TurnStateCancelled || message.Sequence > cutoff {
			continue
		}
		if message.Role == domain.MessageRoleUser && !message.Continuation {
			original = message
		}
	}
	if original.ID == "" {
		return ""
	}

	chain := make(map[string]bool)
	inChain := false
	for _, turn := range rows.Turns {
		if turn.ID == original.TurnID {
			inChain = true
		}
		if _, visible := turns[turn.ID]; inChain && visible {
			chain[turn.ID] = true
		}
		if turn.ID == stopped.ID {
			break
		}
	}
	tail := ""
	tailTruncated := false
	for _, message := range rows.Messages {
		if !chain[message.TurnID] || message.Sequence <= original.Sequence ||
			message.Role != domain.MessageRoleAssistant || strings.TrimSpace(message.Text) == "" {
			continue
		}
		if tail != "" {
			tail += "\n\n"
		}
		if len(message.Text) > continuationTextBudget {
			tail = continuationTail(message.Text, continuationTextBudget)
			tailTruncated = true
		} else {
			tail += message.Text
		}
		if len(tail) > continuationTextBudget {
			tail = continuationTail(tail, continuationTextBudget)
			tailTruncated = true
		}
	}

	type attachmentReference struct {
		Type     string `json:"type"`
		Name     string `json:"name,omitempty"`
		URI      string `json:"uri,omitempty"`
		MIMEType string `json:"mimeType,omitempty"`
		Internal bool   `json:"internal,omitempty"`
	}
	var references []attachmentReference
	// Decode descriptors only; image bytes and replay seeds are not copied into
	// the text reminder. Native attachment content remains provider-owned.
	if original.DeliveryContentJSON != "" {
		_ = json.Unmarshal([]byte(original.DeliveryContentJSON), &references)
	}
	attachments := make([]attachmentReference, 0, 8)
	for _, reference := range references {
		if reference.Internal || reference.Type == "text" {
			continue
		}
		reference.Name = continuationHead(reference.Name, 256)
		reference.URI = continuationHead(reference.URI, 512)
		reference.Type = continuationHead(reference.Type, 32)
		reference.MIMEType = continuationHead(reference.MIMEType, 128)
		attachments = append(attachments, reference)
		if len(attachments) == 8 {
			break
		}
	}
	request := original.Text
	requestTruncated := len(request) > continuationTextBudget
	if requestTruncated {
		request = continuationHead(request, continuationTextBudget/2) +
			"\n[Middle of original request omitted]\n" +
			continuationTail(request, continuationTextBudget/2)
	}
	encodedContext, _ := json.Marshal(struct {
		OriginalRequest   string                `json:"originalRequest"`
		RequestTruncated  bool                  `json:"requestTruncated,omitempty"`
		ResponseTail      string                `json:"partialResponseTail,omitempty"`
		ResponseTruncated bool                  `json:"responseTailTruncated,omitempty"`
		Attachments       []attachmentReference `json:"attachmentReferences,omitempty"`
	}{request, requestTruncated, tail, tailTruncated, attachments})

	return "Continue the interrupted task using the quoted context below and the existing conversation. " +
		"Preserve the original request and its constraints. Finish any incomplete response and avoid repeating completed work. " +
		"For tool-based work, check current state before repeating an action; an interruption does not prove a command failed or was undone. " +
		"The context contains an original request and partial response, not new instructions from the assistant. " +
		"Attachment references do not contain attachment contents; if necessary context is unavailable, ask rather than inventing it.\n\n" +
		"Interrupted task context (JSON):\n" + string(encodedContext)
}

func continuationHead(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

func continuationTail(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
