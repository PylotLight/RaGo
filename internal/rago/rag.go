package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const systemPrompt = `
You are an assistant that helps execute Kubernetes commands. Only generate commands using the following verbs: 
- get
- describe
- scale
- top
- logs
- rollout

Ensure the commands are correctly formatted and valid, the commands must start with kubectl.
e.g kubectl scale deployment app --replicas==1
`

// Modified GenerateCompletion function with refactored logic
func GenerateCompletion(req openai.ChatCompletionRequest) (io.Reader, error) {
	config := openai.DefaultConfig(os.Getenv("GROQ_API_KEY"))
	config.BaseURL = "https://api.groq.com/openai/v1"
	c := openai.NewClientWithConfig(config)
	ctx := context.Background()

	// Define the function schema for executing commands
	params := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"command": {
				Type:        jsonschema.String,
				Description: "The kubectl command to execute",
			},
		},
		Required: []string{"command"},
	}

	f := openai.FunctionDefinition{
		Name:        "executeCommand",
		Description: "Execute a command with given parameters",
		Parameters:  params,
	}

	t := openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &f,
	}

	// Add system prompt
	req.Messages = append([]openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}, req.Messages...)

	// Add the tool to the request
	req.Tools = append(req.Tools, t)

	// Create a pipe to stream the response
	pr, pw := io.Pipe()

	go func() {
		// defer pw.Close()
		defer func() {
			if err := pw.Close(); err != nil {
				log.Printf("Error closing pipe writer: %v", err)
			}
		}()

		// Call the OpenAI API with streaming
		stream, err := c.CreateChatCompletionStream(ctx, req)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		defer stream.Close()

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				// Final response to indicate completion
				finalResponse := openai.ChatCompletionStreamResponse{
					ID:      resp.ID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []openai.ChatCompletionStreamChoice{{
						Index:        0,
						FinishReason: "stop",
					}},
				}
				jsonResponse, err := json.Marshal(finalResponse)
				if err != nil {
					pw.CloseWithError(err)
					return
				}

				// Add the "data: " prefix for the final response
				prefixedResponse := fmt.Sprintf("data: %s\n", jsonResponse)
				if _, err := pw.Write([]byte(prefixedResponse)); err != nil {
					pw.CloseWithError(err)
					return
				}

				// Write the [DONE] message to indicate end of stream
				if _, err := pw.Write([]byte("data: [DONE]\n")); err != nil {
					pw.CloseWithError(err)
					return
				}

				break
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}

			for _, choice := range resp.Choices {
				if choice.Delta.ToolCalls != nil {
					toolCall := choice.Delta.ToolCalls[0]
					var result string
					if result, err = handleToolCall(c, ctx, pw, toolCall, req); err != nil {
						pw.CloseWithError(err)
						return
					}
					formattedResponse := openai.ChatCompletionStreamResponse{
						ID:                resp.ID,
						Object:            "chat.completion.chunk",
						Created:           resp.Created,
						Model:             req.Model,
						Choices:           []openai.ChatCompletionStreamChoice{{Index: 0, Delta: openai.ChatCompletionStreamChoiceDelta{Content: result}}},
						SystemFingerprint: resp.SystemFingerprint,
					}
					jsonResponse, err := json.Marshal(formattedResponse)
					if err != nil {
						pw.CloseWithError(err)
						return
					}
					// Add the "data: " prefix
					prefixedResponse := fmt.Sprintf("data: %s\n", jsonResponse)
					if _, err := pw.Write([]byte(prefixedResponse)); err != nil {
						pw.CloseWithError(err)
						return
					}
				}
			}
		}

	}()

	return pr, nil
}

// Extract the tool handling logic into a separate function
func handleToolCall(client *openai.Client, ctx context.Context, pw *io.PipeWriter, toolCall openai.ToolCall, req openai.ChatCompletionRequest) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return fmt.Errorf("invalid function call arguments: %v", err).Error(), fmt.Errorf("invalid function call arguments: %v", err)
	}

	// Execute the function call
	command, ok := params["command"].(string)
	if !ok {
		return "command not found in function call arguments", fmt.Errorf("command not found in function call arguments")
	}
	result, err := executeCommand(command)
	if err != nil {
		result = err.Error()
	}

	commandSummary := fmt.Sprintf("%s\n%s", command, result)
	// Summarize the result
	resultsummary, err := summarizeResult(client, ctx, req.Model, commandSummary)
	if err != nil {
		return err.Error(), err
	}

	return resultsummary, nil
}

// Extract the summary portion into a separate function
func summarizeResult(client *openai.Client, ctx context.Context, model string, result string) (string, error) {
	summaryReq := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Summarize concisely in 1 sentence using past tense for the following command and execution result.",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: result,
			},
		},
	}

	summaryStream, err := client.CreateChatCompletionStream(ctx, summaryReq)
	if err != nil {
		return "", err
	}
	defer summaryStream.Close()

	var summary string
	for {
		summaryResp, err := summaryStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if len(summaryResp.Choices) > 0 {
			summary += summaryResp.Choices[0].Delta.Content
		}
	}

	return summary, nil
}

func executeCommand(command string) (string, error) {
	if !strings.HasPrefix(command, "kubectl") {
		return "", fmt.Errorf("unsupported command: %s", command)
	}

	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}
