package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/YashVishwas/ixr/pkg/provider"
	"github.com/YashVishwas/ixr/pkg/schema"
)

// bedrockChunkPayload is the outer JSON shape of every "chunk"-type
// event-stream message Bedrock sends: the real event is base64-encoded
// inside "bytes". This wrapper is common to every model Bedrock streams,
// not Anthropic-specific.
type bedrockChunkPayload struct {
	Bytes string `json:"bytes"`
}

// bedrockStreamEvent is the inner, base64-decoded event — the same shape
// and "type" discriminator as native Anthropic's streaming Messages API,
// since Bedrock's Claude endpoint proxies the identical underlying model.
// One struct covers all four event kinds actually needed (message_start,
// content_block_delta, message_delta, message_stop); fields irrelevant to
// a given type are simply absent/zero.
type bedrockStreamEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// streamBedrockEvents reads AWS event-stream frames from r until a clean
// EOF or a message_stop event, dispatching each decoded chunk to fn the
// same way the native Anthropic adapter's Stream does.
func streamBedrockEvents(r io.Reader, requestedModel string, fn func(provider.StreamChunk) error) error {
	var msgID string
	model := requestedModel // message_start's own Model, if present, overrides this
	var inputTok int

	for {
		msg, err := readEventStreamMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("bedrock: read stream event: %w", err)
		}

		if excType := msg.Headers[":exception-type"]; excType != "" {
			return fmt.Errorf("bedrock: stream exception %s: %s", excType, msg.Payload)
		}
		if msg.Headers[":event-type"] != "chunk" {
			continue // e.g. AWS-internal keep-alive/heartbeat events some services send
		}

		var wrapper bedrockChunkPayload
		if err := json.Unmarshal(msg.Payload, &wrapper); err != nil {
			return fmt.Errorf("bedrock: decode chunk envelope: %w", err)
		}
		inner, err := base64.StdEncoding.DecodeString(wrapper.Bytes)
		if err != nil {
			return fmt.Errorf("bedrock: decode chunk base64: %w", err)
		}

		var ev bedrockStreamEvent
		if err := json.Unmarshal(inner, &ev); err != nil {
			return fmt.Errorf("bedrock: decode inner event: %w", err)
		}

		switch ev.Type {
		case "message_start":
			msgID = ev.Message.ID
			if ev.Message.Model != "" {
				model = ev.Message.Model
			}
			inputTok = ev.Message.Usage.InputTokens

		case "content_block_delta":
			if ev.Delta.Type != "text_delta" {
				continue // tool-call streaming isn't supported — see the Stream doc comment's scope note
			}
			if err := fn(provider.StreamChunk{
				ID:    msgID,
				Model: model,
				Delta: schema.Message{Role: "assistant", Content: ev.Delta.Text},
			}); err != nil {
				return err
			}

		case "message_delta":
			u := schema.Usage{
				PromptTokens:     inputTok,
				CompletionTokens: ev.Usage.OutputTokens,
				TotalTokens:      inputTok + ev.Usage.OutputTokens,
			}
			if err := fn(provider.StreamChunk{
				ID:           msgID,
				Model:        model,
				FinishReason: normalizeBedrockStopReason(ev.Delta.StopReason),
				Usage:        &u,
			}); err != nil {
				return err
			}

		case "message_stop":
			return nil
		}
	}
}

// normalizeBedrockStopReason maps Anthropic's stop reasons (unchanged
// as proxied through Bedrock) to OpenAI-shaped finish reasons, matching
// the native Anthropic adapter's normalizeStopReason.
func normalizeBedrockStopReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}
