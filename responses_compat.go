package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const responsesFallbackHeader = "X-Atom2api-Responses-Compat"

type responsesCompatContext struct {
	request map[string]any
}

type chatCompletion struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string         `json:"role"`
			Content   any            `json:"content"`
			Refusal   any            `json:"refusal"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

type chatToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func responsesRequestToChat(payload map[string]json.RawMessage) ([]byte, *responsesCompatContext, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	var request map[string]any
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, nil, err
	}

	if err := validateResponsesFallbackRequest(request); err != nil {
		return nil, nil, err
	}
	messages, err := responsesInputToMessages(request["input"])
	if err != nil {
		return nil, nil, err
	}
	if instructions, ok := request["instructions"].(string); ok && strings.TrimSpace(instructions) != "" {
		messages = append([]any{map[string]any{"role": "system", "content": instructions}}, messages...)
	}
	messages = normalizeChatMessages(messages)
	if len(messages) == 0 {
		return nil, nil, errors.New("input must contain at least one message")
	}

	chat := map[string]any{
		"model":    request["model"],
		"messages": messages,
	}
	copyResponseRequestField(chat, request, "stream")
	copyResponseRequestField(chat, request, "temperature")
	copyResponseRequestField(chat, request, "top_p")
	copyResponseRequestField(chat, request, "parallel_tool_calls")
	copyResponseRequestField(chat, request, "service_tier")
	copyResponseRequestField(chat, request, "user")
	if value, ok := request["max_output_tokens"]; ok {
		chat["max_tokens"] = value
	}
	if reasoning, ok := request["reasoning"].(map[string]any); ok {
		if effort, exists := reasoning["effort"]; exists && effort != nil {
			chat["reasoning_effort"] = effort
		}
	}
	if tools, exists := request["tools"]; exists {
		items, ok := tools.([]any)
		if !ok {
			return nil, nil, errors.New("tools must be an array")
		}
		converted, convertErr := responsesToolsToChat(items)
		if convertErr != nil {
			return nil, nil, convertErr
		}
		if len(converted) > 0 {
			chat["tools"] = converted
		}
	}
	if choice, exists := request["tool_choice"]; exists {
		converted, convertErr := responsesToolChoiceToChat(choice)
		if convertErr != nil {
			return nil, nil, convertErr
		}
		chat["tool_choice"] = converted
	}
	if text, ok := request["text"].(map[string]any); ok {
		if format, exists := text["format"]; exists {
			converted, convertErr := responsesTextFormatToChat(format)
			if convertErr != nil {
				return nil, nil, convertErr
			}
			if converted != nil {
				chat["response_format"] = converted
			}
		}
	}
	if streaming, _ := request["stream"].(bool); streaming {
		chat["stream_options"] = map[string]any{"include_usage": true}
	}

	body, err := json.Marshal(chat)
	if err != nil {
		return nil, nil, err
	}
	return body, &responsesCompatContext{request: request}, nil
}

func validateResponsesFallbackRequest(request map[string]any) error {
	allowed := map[string]bool{
		"model": true, "input": true, "instructions": true, "stream": true,
		"temperature": true, "top_p": true, "max_output_tokens": true,
		"tools": true, "tool_choice": true, "parallel_tool_calls": true,
		"text": true, "reasoning": true, "metadata": true, "store": true,
		"service_tier": true, "user": true, "include": true,
		"prompt_cache_key": true,
	}
	for name, value := range request {
		if !allowed[name] && value != nil {
			return fmt.Errorf("%s is not supported by the Responses-to-Chat compatibility layer", name)
		}
	}
	if value, exists := request["store"]; exists {
		if store, ok := value.(bool); !ok || store {
			return errors.New("store=true is not supported by the Responses-to-Chat compatibility layer")
		}
	}
	if instructions, exists := request["instructions"]; exists && instructions != nil {
		if _, ok := instructions.(string); !ok {
			return errors.New("instructions must be a string when using the Responses-to-Chat compatibility layer")
		}
	}
	return nil
}

func responsesInputToMessages(input any) ([]any, error) {
	switch value := input.(type) {
	case string:
		return []any{map[string]any{"role": "user", "content": value}}, nil
	case []any:
		return responsesItemsToMessages(value)
	case map[string]any:
		return responsesItemsToMessages([]any{value})
	case nil:
		return nil, errors.New("input is required")
	default:
		return nil, errors.New("input must be a string, message, or array of input items")
	}
}

func responsesItemsToMessages(items []any) ([]any, error) {
	messages := make([]any, 0, len(items))
	pendingCalls := make([]any, 0)
	flushCalls := func() {
		if len(pendingCalls) == 0 {
			return
		}
		messages = append(messages, map[string]any{
			"role": "assistant", "content": nil, "tool_calls": pendingCalls,
		})
		pendingCalls = nil
	}

	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("every input item must be an object")
		}
		itemType, _ := item["type"].(string)
		switch itemType {
		case "function_call":
			name, _ := item["name"].(string)
			arguments, _ := item["arguments"].(string)
			callID, _ := item["call_id"].(string)
			if name == "" || callID == "" {
				return nil, errors.New("function_call input items require name and call_id")
			}
			pendingCalls = append(pendingCalls, map[string]any{
				"id": callID, "type": "function",
				"function": map[string]any{"name": name, "arguments": arguments},
			})
		case "function_call_output":
			flushCalls()
			callID, _ := item["call_id"].(string)
			if callID == "" {
				return nil, errors.New("function_call_output input items require call_id")
			}
			messages = append(messages, map[string]any{
				"role": "tool", "tool_call_id": callID, "content": stringifyContent(item["output"]),
			})
		case "reasoning":
			// Reasoning items are model-internal context and have no Chat Completions equivalent.
			continue
		case "message", "":
			flushCalls()
			role, _ := item["role"].(string)
			if role == "" {
				return nil, errors.New("message input items require role")
			}
			if role == "developer" {
				role = "system"
			}
			content, err := responsesContentToChat(item["content"])
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{"role": role, "content": content})
		default:
			return nil, fmt.Errorf("input item type %q is not supported by the Responses-to-Chat compatibility layer", itemType)
		}
	}
	flushCalls()
	return messages, nil
}

func responsesContentToChat(content any) (any, error) {
	if text, ok := content.(string); ok {
		return text, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return nil, errors.New("message content must be a string or an array")
	}
	converted := make([]any, 0, len(parts))
	allText := true
	var joined strings.Builder
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("message content parts must be objects")
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "input_text", "output_text", "text":
			text, _ := part["text"].(string)
			joined.WriteString(text)
			converted = append(converted, map[string]any{"type": "text", "text": text})
		case "input_image", "image_url":
			allText = false
			imageURL, _ := part["image_url"].(string)
			if imageURL == "" {
				return nil, errors.New("input_image requires image_url; file_id images are not supported")
			}
			image := map[string]any{"url": imageURL}
			if detail, ok := part["detail"].(string); ok && detail != "" {
				image["detail"] = detail
			}
			converted = append(converted, map[string]any{"type": "image_url", "image_url": image})
		case "refusal":
			text, _ := part["refusal"].(string)
			joined.WriteString(text)
			converted = append(converted, map[string]any{"type": "text", "text": text})
		default:
			return nil, fmt.Errorf("content part type %q is not supported by the Responses-to-Chat compatibility layer", partType)
		}
	}
	if allText {
		return joined.String(), nil
	}
	return converted, nil
}

func responsesToolsToChat(tools []any) ([]any, error) {
	converted := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("tools must contain objects")
		}
		toolType, _ := tool["type"].(string)
		if toolType != "function" {
			return nil, fmt.Errorf("tool type %q is not supported by the Responses-to-Chat compatibility layer", toolType)
		}
		name, _ := tool["name"].(string)
		if name == "" {
			return nil, errors.New("function tools require name")
		}
		function := map[string]any{"name": name}
		for _, field := range []string{"description", "parameters", "strict"} {
			if value, exists := tool[field]; exists {
				function[field] = value
			}
		}
		converted = append(converted, map[string]any{"type": "function", "function": function})
	}
	return converted, nil
}

func responsesToolChoiceToChat(choice any) (any, error) {
	if value, ok := choice.(string); ok {
		return value, nil
	}
	object, ok := choice.(map[string]any)
	if !ok {
		return nil, errors.New("tool_choice must be a string or object")
	}
	choiceType, _ := object["type"].(string)
	if choiceType != "function" {
		return nil, fmt.Errorf("tool_choice type %q is not supported by the Responses-to-Chat compatibility layer", choiceType)
	}
	name, _ := object["name"].(string)
	if name == "" {
		return nil, errors.New("function tool_choice requires name")
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": name}}, nil
}

func responsesTextFormatToChat(value any) (any, error) {
	format, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("text.format must be an object")
	}
	formatType, _ := format["type"].(string)
	switch formatType {
	case "", "text":
		return nil, nil
	case "json_object":
		return map[string]any{"type": "json_object"}, nil
	case "json_schema":
		schema := map[string]any{}
		for _, field := range []string{"name", "description", "schema", "strict"} {
			if item, exists := format[field]; exists {
				schema[field] = item
			}
		}
		if schema["name"] == nil || schema["schema"] == nil {
			return nil, errors.New("json_schema text format requires name and schema")
		}
		return map[string]any{"type": "json_schema", "json_schema": schema}, nil
	default:
		return nil, fmt.Errorf("text format type %q is not supported by the Responses-to-Chat compatibility layer", formatType)
	}
}

func chatResponseToResponses(body []byte, model string, context *responsesCompatContext) ([]byte, tokenUsage, error) {
	var completion chatCompletion
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, tokenUsage{}, fmt.Errorf("decode Chat Completions response: %w", err)
	}
	if completion.ID == "" {
		return nil, tokenUsage{}, errors.New("Chat Completions response is missing id")
	}
	usage := extractUsage(body)
	outputs := make([]any, 0)
	status := "completed"
	var incompleteDetails any
	for _, choice := range completion.Choices {
		if choice.FinishReason == "length" {
			status = "incomplete"
			incompleteDetails = map[string]any{"reason": "max_output_tokens"}
		} else if choice.FinishReason == "content_filter" {
			status = "incomplete"
			incompleteDetails = map[string]any{"reason": "content_filter"}
		}
		if text := chatContentText(choice.Message.Content); text != "" || len(choice.Message.ToolCalls) == 0 {
			outputs = append(outputs, responseMessageItem(randomID("msg"), status, text))
		}
		for _, toolCall := range choice.Message.ToolCalls {
			outputs = append(outputs, responseFunctionCallItem(toolCall))
		}
	}
	created := completion.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	responseID := responseIDFromChat(completion.ID)
	response := buildResponsesEnvelope(context, responseID, created, status, model, outputs, responsesUsage(usage))
	response["incomplete_details"] = incompleteDetails
	response["completed_at"] = created
	encoded, err := json.Marshal(response)
	return encoded, usage, err
}

func responseMessageItem(id, status, text string) map[string]any {
	return map[string]any{
		"id": id, "type": "message", "status": status, "role": "assistant",
		"content": []any{map[string]any{
			"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{},
		}},
	}
}

func responseFunctionCallItem(toolCall chatToolCall) map[string]any {
	callID := toolCall.ID
	if callID == "" {
		callID = randomID("call")
	}
	return map[string]any{
		"id": randomID("fc"), "type": "function_call", "status": "completed",
		"call_id": callID, "name": toolCall.Function.Name, "arguments": toolCall.Function.Arguments,
	}
}

func buildResponsesEnvelope(context *responsesCompatContext, id string, created int64, status, model string, output []any, usage any) map[string]any {
	request := map[string]any{}
	if context != nil && context.request != nil {
		request = context.request
	}
	response := map[string]any{
		"id": id, "object": "response", "created_at": created, "status": status,
		"background": false, "completed_at": nil, "conversation": nil, "error": nil,
		"incomplete_details": nil, "instructions": valueOrNil(request, "instructions"),
		"max_output_tokens": valueOrNil(request, "max_output_tokens"), "max_tool_calls": nil,
		"metadata": valueOrDefault(request, "metadata", map[string]any{}), "model": model,
		"output": output, "parallel_tool_calls": valueOrDefault(request, "parallel_tool_calls", true),
		"previous_response_id": nil, "prompt": nil, "prompt_cache_key": valueOrNil(request, "prompt_cache_key"),
		"reasoning":         valueOrDefault(request, "reasoning", map[string]any{"effort": nil, "summary": nil}),
		"safety_identifier": nil, "service_tier": valueOrDefault(request, "service_tier", "default"),
		"store": false, "temperature": valueOrDefault(request, "temperature", 1.0),
		"text":        valueOrDefault(request, "text", map[string]any{"format": map[string]any{"type": "text"}}),
		"tool_choice": valueOrDefault(request, "tool_choice", "auto"),
		"tools":       valueOrDefault(request, "tools", []any{}), "top_logprobs": 0,
		"top_p": valueOrDefault(request, "top_p", 1.0), "truncation": "disabled", "usage": usage,
		"user": valueOrNil(request, "user"),
	}
	return response
}

func responsesUsage(usage tokenUsage) map[string]any {
	return map[string]any{
		"input_tokens":          usage.Input,
		"input_tokens_details":  map[string]any{"cached_tokens": usage.Cached},
		"output_tokens":         usage.Output,
		"output_tokens_details": map[string]any{"reasoning_tokens": usage.Reasoning},
		"total_tokens":          usage.Input + usage.Output,
	}
}

type responsesStreamState struct {
	context      *responsesCompatContext
	model        string
	responseID   string
	created      int64
	sequence     int64
	started      bool
	message      *responsesStreamMessage
	tools        map[int]*responsesStreamTool
	outputCount  int
	usage        tokenUsage
	finishReason string
}

type responsesStreamMessage struct {
	id          string
	outputIndex int
	text        strings.Builder
}

type responsesStreamTool struct {
	id          string
	callID      string
	name        string
	arguments   strings.Builder
	outputIndex int
}

type chatStreamChunk struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   any            `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason any `json:"finish_reason"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

func (p *Proxy) streamChatAsResponses(w http.ResponseWriter, response *http.Response, route ModelRoute, context *responsesCompatContext, markFirstToken func()) (tokenUsage, string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "streaming is not supported by this server")
		return tokenUsage{}, "streaming is not supported by this server"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)

	state := &responsesStreamState{context: context, model: route.Requested, tools: map[int]*responsesStreamTool{}}
	reader := bufio.NewReader(response.Body)
	errorText := ""
	for {
		line, err := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("data:")) {
			data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
			if bytes.Equal(data, []byte("[DONE]")) {
				if finishErr := state.finish(w, flusher); finishErr != nil {
					errorText = finishErr.Error()
				}
				break
			}
			if len(data) > 0 {
				if streamChunkHasOutput(data) {
					markFirstToken()
				}
				if chunkErr := state.consume(w, flusher, data); chunkErr != nil {
					errorText = chunkErr.Error()
					break
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if errorText == "" {
					if finishErr := state.finish(w, flusher); finishErr != nil {
						errorText = finishErr.Error()
					}
				}
			} else {
				errorText = err.Error()
			}
			break
		}
	}
	return state.usage, errorText
}

func (s *responsesStreamState) consume(w io.Writer, flusher http.Flusher, data []byte) error {
	var chunk chatStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("decode Chat Completions stream: %w", err)
	}
	if !s.started {
		s.responseID = responseIDFromChat(chunk.ID)
		s.created = chunk.Created
		if s.created == 0 {
			s.created = time.Now().Unix()
		}
		if err := s.start(w, flusher); err != nil {
			return err
		}
	}
	if len(chunk.Usage) > 0 && !bytes.Equal(bytes.TrimSpace(chunk.Usage), []byte("null")) {
		usageEnvelope, _ := json.Marshal(map[string]json.RawMessage{"usage": chunk.Usage})
		s.usage = extractUsage(usageEnvelope)
	}
	for _, choice := range chunk.Choices {
		if finishReason, ok := choice.FinishReason.(string); ok && finishReason != "" {
			s.finishReason = finishReason
		}
		if content := chatContentText(choice.Delta.Content); content != "" {
			if err := s.addText(w, flusher, content); err != nil {
				return err
			}
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			if err := s.addToolCall(w, flusher, toolCall); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *responsesStreamState) start(w io.Writer, flusher http.Flusher) error {
	s.started = true
	created := buildResponsesEnvelope(s.context, s.responseID, s.created, "in_progress", s.model, []any{}, nil)
	if err := s.event(w, flusher, "response.created", map[string]any{"response": created}); err != nil {
		return err
	}
	return s.event(w, flusher, "response.in_progress", map[string]any{"response": created})
}

func (s *responsesStreamState) addText(w io.Writer, flusher http.Flusher, delta string) error {
	if s.message == nil {
		s.message = &responsesStreamMessage{id: randomID("msg"), outputIndex: s.outputCount}
		s.outputCount++
		item := map[string]any{
			"id": s.message.id, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{},
		}
		if err := s.event(w, flusher, "response.output_item.added", map[string]any{"output_index": s.message.outputIndex, "item": item}); err != nil {
			return err
		}
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}
		if err := s.event(w, flusher, "response.content_part.added", map[string]any{
			"item_id": s.message.id, "output_index": s.message.outputIndex, "content_index": 0, "part": part,
		}); err != nil {
			return err
		}
	}
	s.message.text.WriteString(delta)
	return s.event(w, flusher, "response.output_text.delta", map[string]any{
		"item_id": s.message.id, "output_index": s.message.outputIndex, "content_index": 0,
		"delta": delta, "logprobs": []any{},
	})
}

func (s *responsesStreamState) addToolCall(w io.Writer, flusher http.Flusher, delta chatToolCall) error {
	tool := s.tools[delta.Index]
	if tool == nil {
		callID := delta.ID
		if callID == "" {
			callID = randomID("call")
		}
		tool = &responsesStreamTool{
			id: randomID("fc"), callID: callID, name: delta.Function.Name, outputIndex: s.outputCount,
		}
		s.outputCount++
		s.tools[delta.Index] = tool
		item := map[string]any{
			"id": tool.id, "type": "function_call", "status": "in_progress", "call_id": tool.callID,
			"name": tool.name, "arguments": "",
		}
		if err := s.event(w, flusher, "response.output_item.added", map[string]any{"output_index": tool.outputIndex, "item": item}); err != nil {
			return err
		}
	} else {
		if delta.ID != "" {
			tool.callID = delta.ID
		}
		if delta.Function.Name != "" {
			tool.name += delta.Function.Name
		}
	}
	if delta.Function.Arguments == "" {
		return nil
	}
	tool.arguments.WriteString(delta.Function.Arguments)
	return s.event(w, flusher, "response.function_call_arguments.delta", map[string]any{
		"item_id": tool.id, "output_index": tool.outputIndex, "delta": delta.Function.Arguments,
	})
}

func (s *responsesStreamState) finish(w io.Writer, flusher http.Flusher) error {
	if !s.started {
		return errors.New("Chat Completions stream ended before the first response chunk")
	}
	status := "completed"
	var incompleteDetails any
	if s.finishReason == "length" {
		status = "incomplete"
		incompleteDetails = map[string]any{"reason": "max_output_tokens"}
	} else if s.finishReason == "content_filter" {
		status = "incomplete"
		incompleteDetails = map[string]any{"reason": "content_filter"}
	}
	outputs := make([]indexedOutput, 0, s.outputCount)
	if s.message != nil {
		text := s.message.text.String()
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
		if err := s.event(w, flusher, "response.output_text.done", map[string]any{
			"item_id": s.message.id, "output_index": s.message.outputIndex, "content_index": 0, "text": text, "logprobs": []any{},
		}); err != nil {
			return err
		}
		if err := s.event(w, flusher, "response.content_part.done", map[string]any{
			"item_id": s.message.id, "output_index": s.message.outputIndex, "content_index": 0, "part": part,
		}); err != nil {
			return err
		}
		item := responseMessageItem(s.message.id, status, text)
		if err := s.event(w, flusher, "response.output_item.done", map[string]any{"output_index": s.message.outputIndex, "item": item}); err != nil {
			return err
		}
		outputs = append(outputs, indexedOutput{index: s.message.outputIndex, item: item})
	}
	toolIndexes := make([]int, 0, len(s.tools))
	for index := range s.tools {
		toolIndexes = append(toolIndexes, index)
	}
	sort.Ints(toolIndexes)
	for _, index := range toolIndexes {
		tool := s.tools[index]
		arguments := tool.arguments.String()
		if err := s.event(w, flusher, "response.function_call_arguments.done", map[string]any{
			"item_id": tool.id, "output_index": tool.outputIndex, "arguments": arguments,
		}); err != nil {
			return err
		}
		item := map[string]any{
			"id": tool.id, "type": "function_call", "status": "completed", "call_id": tool.callID,
			"name": tool.name, "arguments": arguments,
		}
		if err := s.event(w, flusher, "response.output_item.done", map[string]any{"output_index": tool.outputIndex, "item": item}); err != nil {
			return err
		}
		outputs = append(outputs, indexedOutput{index: tool.outputIndex, item: item})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].index < outputs[j].index })
	finalOutput := make([]any, 0, len(outputs))
	for _, output := range outputs {
		finalOutput = append(finalOutput, output.item)
	}
	response := buildResponsesEnvelope(s.context, s.responseID, s.created, status, s.model, finalOutput, responsesUsage(s.usage))
	response["completed_at"] = time.Now().Unix()
	response["incomplete_details"] = incompleteDetails
	eventType := "response.completed"
	if status == "incomplete" {
		eventType = "response.incomplete"
	}
	return s.event(w, flusher, eventType, map[string]any{"response": response})
}

type indexedOutput struct {
	index int
	item  any
}

func (s *responsesStreamState) event(w io.Writer, flusher http.Flusher, eventType string, fields map[string]any) error {
	fields["type"] = eventType
	fields["sequence_number"] = s.sequence
	s.sequence++
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func chatContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var text strings.Builder
		for _, raw := range value {
			part, _ := raw.(map[string]any)
			if partText, ok := part["text"].(string); ok {
				text.WriteString(partText)
			}
		}
		return text.String()
	default:
		return ""
	}
}

func responseIDFromChat(chatID string) string {
	trimmed := strings.TrimPrefix(chatID, "chatcmpl-")
	trimmed = strings.TrimPrefix(trimmed, "chatcmpl_")
	if trimmed == "" {
		return randomID("resp")
	}
	return "resp_" + trimmed
}

func copyResponseRequestField(destination, source map[string]any, name string) {
	if value, exists := source[name]; exists && value != nil {
		destination[name] = value
	}
}

func valueOrNil(values map[string]any, name string) any {
	if value, exists := values[name]; exists {
		return value
	}
	return nil
}

func valueOrDefault(values map[string]any, name string, fallback any) any {
	if value, exists := values[name]; exists && value != nil {
		return value
	}
	return fallback
}

func stringifyContent(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
