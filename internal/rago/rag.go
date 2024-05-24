package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	// "github.com/tmc/langchaingo/jsonschema"
	// "github.com/tmc/langchaingo/llms"

	// "github.com/tmc/langchaingo/llms/openai"

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

Ensure the commands are correctly formatted and valid.
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
		defer pw.Close()

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
				break
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}

			for _, choice := range resp.Choices {
				if choice.Delta.ToolCalls != nil {
					toolCall := choice.Delta.ToolCalls[0]
					if err := handleToolCall(c, ctx, pw, toolCall, req); err != nil {
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
func handleToolCall(client *openai.Client, ctx context.Context, pw *io.PipeWriter, toolCall openai.ToolCall, req openai.ChatCompletionRequest) error {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return fmt.Errorf("invalid function call arguments: %v", err)
	}

	// Execute the function call
	command, ok := params["command"].(string)
	if !ok {
		return fmt.Errorf("command not found in function call arguments")
	}
	result, err := executeCommand(command)
	if err != nil {
		return fmt.Errorf("error executing command: %v", err)
	}

	// Write the result to the stream
	if _, err := pw.Write([]byte(result)); err != nil {
		return err
	}

	// Summarize the result
	summary, err := summarizeResult(client, ctx, req.Model, result)
	if err != nil {
		return err
	}

	// Write the summary to the stream
	if _, err := pw.Write([]byte("\n\nSummary: " + summary)); err != nil {
		return err
	}

	return nil
}

// Extract the summary portion into a separate function
func summarizeResult(client *openai.Client, ctx context.Context, model string, result string) (string, error) {
	summaryReq := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Summarize the following command execution result:",
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
