package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"rago/internal/utils"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

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
			if err := chat_loop(c, ctx, pw, &resp, &req); err != nil {
				pw.CloseWithError(err)
				return
			}
		}

	}()

	return pr, nil
}

func chat_loop(c *openai.Client, ctx context.Context, pw *io.PipeWriter, resp *openai.ChatCompletionStreamResponse, req *openai.ChatCompletionRequest) error {
	for _, choice := range resp.Choices {
		var result string
		fmt.Printf("%+v\n", resp.Choices)
		switch len(choice.Delta.ToolCalls) {
		case 1:
			// Tool choice is made an executed returning the result
			tool_result, err := handle_ToolCall(c, ctx, &choice.Delta.ToolCalls[0], req)
			if err != nil {
				// pw.CloseWithError(err)
				return err
			}
			result = tool_result
		default:
			result = choice.Delta.Content
			if strings.Contains(result, "PAUSE") {
				// Create another chat stream to evaluate the planned action before the PAUSE and execute if accurate.
				// Call the OpenAI API with streaming
				// req.Messages = append(req.Messages, )
				stream, err := c.CreateChatCompletionStream(ctx, *req)
				if err != nil {
					return err
				}
				defer stream.Close()
				chat_loop(c, ctx, pw, resp, req)
			}
		}
		utils.WriteResponse(result, pw, req, resp)
	}
	return nil
}

// Extract the tool handling logic into a separate function
func handle_ToolCall(client *openai.Client, ctx context.Context, toolCall *openai.ToolCall, req *openai.ChatCompletionRequest) (string, error) {
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
	case "executeCommand":
		// Execute the function call
		command, ok := params["command"].(string)
		if !ok {
			return "command not found in function call arguments", fmt.Errorf("command not found in function call arguments")
		}
		println("command:", command)
		result, err := utils.ExecuteCommand(command)
		if err != nil {
			result = err.Error()
		}

		commandSummary = fmt.Sprintf("Prompt: %s\n\nCommand: %s\n\nResult: %s", usersPrompt, command, result)

	case "controlLights":
		light_name := params["light_name"].(string)
		state := params["state"].(bool)

		commandSummary = utils.UpdateLight(light_name, state)
	}

	// println(commandSummary)
	resultsummary, err := summarizeResult(client, ctx, req.Model, commandSummary)
	if err != nil {
		return err.Error(), err
	}

	return resultsummary, nil
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
				Type:        jsonschema.Boolean,
				Description: "The state to set the light to on (true) or off (false)",
			},
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
	const defaultPrompt = `
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
	const reActPrompt = `
	You are a Question Answering AI with reasoning ability and ability to execute commands via tools.
	You will receive a Question from the User.
	In order to answer any Question, you run in a loop of Thought, Action, PAUSE, Observation.
	If from the Thought or Observation you can derive the answer to the Question, you MUST also output an "Answer: ", followed by the answer and the answer ONLY, without explanation of the steps used to arrive at the answer.
	You will use "Thought: " to describe your thoughts about the question being asked.
	You will use "Action: " to run one of the actions available to you - then return PAUSE. NEVER continue generating "Observation: " or "Answer: " in the same response that contains PAUSE.
	"Observation" will be presented to you as the result of previous "Action".
	If the "Observation" you received is not related to the question asked, or you cannot derive the answer from the observation, change the Action to be performed and try again.
	If the Question or action ever requires a specific pod name, use the Pod search formula to pull and match the correct pod name.
	Your available "Actions" are:
	- Kubernetes: Execute a Kubernetes command (e.g., kubectl get pods)
	- Match: Pod search formula for matching user prompt for a pod to a specific kubernetes pod name (e.g, $(kubectl get pods --no-headers=true | grep "app" | awk '{print $1}' | head -1)) 
	- System: Execute a linux system command (e.g., free -h, grep, awk '{print $1}')
	- Network: Execute a network command (e.g., ping google.com)
	- Search: Run a google search to pull relevant results from the internet
	- Lifx: Control a smart light (e.g., bedroom off)
	Examples:
	Question: Can you get the logs for the pod named "my-pod"?
	Thought: I should use a linux grep filter to match the pod name to get the logs.
	Action: kubectl logs $(kubectl get pods | grep my-pod | awk '{print $1}' | head -1) | tail -50
	Question: Can you delete the jellyfin pod?
	Thought: I need to find the exact name of the jellyfin pod first by using the "Pod search formula"
	Action: echo $(kubectl get pods --no-headers=true | grep jellyfin | awk '{print $1}' | head -1)
	Observation: jellyfin-xyz123
	Thought: I found the pod named "jellyfin". Now I can delete it.
	Action: kubectl delete pod jellyfin-xyz123
	`
	// Add system prompt
	req.Messages = append([]openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: reActPrompt,
		},
	}, req.Messages...)

	// Add the tool to the request
	req.Tools = tools
}

// Extract the summary portion into a separate function
func summarizeResult(client *openai.Client, ctx context.Context, model string, result string) (string, error) {
	summaryReq := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: `Provide a concise and clear answer to the user's prompt by using the executed command and its result. 
				Ensure the answer directly confirms the action taken and includes the outcome of the command and NEVER repeat the question or summary prompt.
				If there are any errors, make sure to include the full details including commands run`,
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
