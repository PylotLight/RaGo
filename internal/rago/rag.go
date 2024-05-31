package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"

	"rago/internal/lifx"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const cmdPrompt = `
You are an assistant that helps execute various system and Kubernetes commands. Your primary goal is to ensure commands are correctly formatted and valid.
Never repeat the question prompt.
Basic Kubernetes Commands must start with 'kubectl' for Kubernetes to pull basic information or update deployment scaling. Examples:
- kubectl get pods
- kubectl describe node
- kubectl scale deployment app --replicas==1
- kubectl logs app

You can also run general system commands. Examples:
- free -h
- df -h

Ensure the output is correct and complete. If additional information is needed, perform the necessary intermediary steps to gather required details.

For multi-step processes, think through the sequence of commands needed to achieve the final goal and execute them accordingly. For example, if a specific pod's logs are requested but not provided, first list the pods using '$(kubectl get pods | grep app | awk '{print $1}' | head -1)' to find the relevant name, then retrieve the logs for that pod.

When a specific pod name is needed, use multiple inline commands, ensure they are correctly formatted. Examples:
- kubectl describe pod $(kubectl get pods --no-headers=true | grep app | awk '{print $1}' | head -1)
- kubectl logs $(kubectl get pods | grep app | awk '{print $1}' | head -1) | tail -50
- free -h | awk '{print $1, $2, $3}'
`

// Modified GenerateCompletion function with refactored logic
func GenerateCompletion(req openai.ChatCompletionRequest, token string) (io.Reader, error) {
	config := openai.DefaultConfig(token)
	config.BaseURL = "https://api.groq.com/openai/v1"
	c := openai.NewClientWithConfig(config)
	ctx := context.Background()

	// Create a pipe to stream the response
	pr, pw := io.Pipe()

	go func() {
		// defer pw.Close()
		defer func() {
			if err := pw.Close(); err != nil {
				log.Printf("Error closing pipe writer: %v", err)
			}
		}()

		addToolDefinitions(&req)

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
					// Tool choice is made an executed returning the result
					if result, err = handleToolCall(c, ctx, toolCall, req); err != nil {
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
func handleToolCall(client *openai.Client, ctx context.Context, toolCall openai.ToolCall, req openai.ChatCompletionRequest) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return fmt.Errorf("invalid function call arguments: %v", err).Error(), fmt.Errorf("invalid function call arguments: %v", err)
	}
	var usersPrompt, commandSummary string
	for _, message := range req.Messages {
		if message.Role == "user" {
			usersPrompt = message.Content
		}
	}

	switch toolCall.Function.Name {
	case "kube":
		// Execute the function call
		command, ok := params["command"].(string)
		if !ok {
			return "command not found in function call arguments", fmt.Errorf("command not found in function call arguments")
		}
		result, err := executeCommand(command)
		if err != nil {
			result = err.Error()
		}

		commandSummary = fmt.Sprintf("Prompt: %s\n\nCommand: %s\n\nResult: %s", usersPrompt, command, result)

	case "controlLights":
		light_name := params["light_name"].(string)
		state := params["state"].(bool)

		commandSummary = lifx.UpdateLight(light_name, state)
	}

	// println(commandSummary)
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
				Content: "Provide a concise and clear answer to the user's prompt by using the executed command and its result. Ensure the answer directly confirms the action taken and includes the outcome of the command without repeating the question.",
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

// Execute server commands
func executeCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// Add tool schema defintions
func addToolDefinitions(req *openai.ChatCompletionRequest) {
	lifxParams := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"light_name": {
				Type:        jsonschema.String,
				Enum:        []string{"bedroom", "living room"},
				Description: "The ID or name of the light to control",
			},
			"state": {
				Type: jsonschema.Boolean,
				// Enum:        []bool{true, false},
				Description: "The state to set the light to on (true) or off (false)",
			},
			// "color": {
			// 	Type: jsonschema.Object,
			// 	Properties: map[string]jsonschema.Definition{
			// 		"hue": {
			// 			Type:        jsonschema.Integer,
			// 			Description: "The hue of the light (0-65535)",
			// 		},
			// 		"saturation": {
			// 			Type:        jsonschema.Integer,
			// 			Description: "The saturation of the light (0-65535)",
			// 		},
			// 		"brightness": {
			// 			Type:        jsonschema.Integer,
			// 			Description: "The brightness of the light (0-65535)",
			// 		},
			// 		"kelvin": {
			// 			Type:        jsonschema.Integer,
			// 			Description: "The color temperature of the light (2500-9000)",
			// 		},
			// 	},
			// 	Required:    []string{"hue", "saturation", "brightness", "kelvin"},
			// 	Description: "The color settings of the light in HSBK format",
			// },
		},
		Required: []string{"light_name", "state"},
	}

	// Define the function schema for executing commands
	cmdParams := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"command": {
				Type:        jsonschema.String,
				Description: "The server command to execute",
			},
		},
		Required: []string{"command"},
	}

	cmdFunc := openai.FunctionDefinition{
		Name:        "executeCommand",
		Description: "Execute a command with given parameters",
		Parameters:  cmdParams,
	}
	lifxFunc := openai.FunctionDefinition{
		Name:        "controlLights",
		Description: "Control lifx lights with given parameters to turn them on or off",
		Parameters:  lifxParams,
	}
	tools := []openai.Tool{{
		Type:     openai.ToolTypeFunction,
		Function: &cmdFunc,
	}, {
		Type:     openai.ToolTypeFunction,
		Function: &lifxFunc,
	}}

	// Add system prompt
	req.Messages = append([]openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: cmdPrompt,
		},
	}, req.Messages...)

	// Add the tool to the request
	req.Tools = tools
}
