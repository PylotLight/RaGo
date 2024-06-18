package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"

	"github.com/2tvenom/golifx"
	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type ChatContext struct {
	Client       *openai.Client
	Ctx          context.Context
	PWriter      *io.PipeWriter
	PReader      *io.PipeReader
	Req          *openai.ChatCompletionRequest
	Resp         *openai.ChatCompletionStreamResponse
	PromptResult string
	UserPrompt   string
}

func (cc *ChatContext) Process_ChatStream() error {
	stream, err := cc.Client.CreateChatCompletionStream(cc.Ctx, *cc.Req)
	if err != nil {
		return err
	}
	defer stream.Close()
	for {
		resp, err := stream.Recv()
		cc.Resp = &resp
		if err == io.EOF {
			// Write the [DONE] message to indicate end of stream
			if _, err := cc.PWriter.Write([]byte("data: [DONE]\n")); err != nil {
				return cc.PWriter.CloseWithError(err)
			}
			break
		}
		if err != nil {
			return cc.PWriter.CloseWithError(err)
		}
		if err := cc.chat_loop(); err != nil {
			return cc.PWriter.CloseWithError(err)
		}
	}
	return nil
}

func (cc *ChatContext) chat_loop() error {
	// Action handlers
	actionHandlers := map[string]func(){
		"Command": func() {
			cc.addToolDefinitions("command")
			cc.Process_ChatStream()
		},
		"Lifx": func() {
			cc.addToolDefinitions("lifx")
			cc.Process_ChatStream()
		},
		"Search": func() {
			println("Search internet and run chat loop again")
			// cc.addToolDefinitions("search")
			// cc.Process_ChatStream()
		},
	}

	// Helper function to check and handle actions
	checkAndHandleActions := func(result string) (handled bool, err error) {
		for action, handler := range actionHandlers {
			// Search[Latest Go version including minor versions] run with action here or something?
			if strings.EqualFold(result, action) {
				handler()
				return true, nil
			}
		}
		return false, nil
	}

	for _, choice := range cc.Resp.Choices {
		cc.PromptResult = cc.PromptResult + choice.Delta.Content
		if strings.Contains(cc.PromptResult, "PAUSE") {
			cc.UserPrompt = cc.PromptResult
			matches := regexp.MustCompile(`Action: (?P<action>\w+)\[.*?\]`).FindStringSubmatch(cc.PromptResult)
			if len(matches) > 0 {
				cc.PromptResult = ""
				handled, err := checkAndHandleActions(matches[1])
				if err != nil {
					cc.PWriter.CloseWithError(err)
					return err
				}
				if handled {
					return nil
				}
			}

		}

		// Process tool calls if present
		if len(choice.Delta.ToolCalls) > 0 {
			toolResult, err := cc.handle_ToolCall(&choice.Delta.ToolCalls[0])
			if err != nil {
				cc.PWriter.CloseWithError(err)
				return err
			}
			writeResponse(toolResult, cc.PWriter, cc.Req, cc.Resp)
		}

	}
	// writeResponse(cc.PromptResult, cc.PWriter, cc.Req, cc.Resp)
	return nil
}

// Extract the tool handling logic into a separate function
func (cc *ChatContext) handle_ToolCall(toolCall *openai.ToolCall) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return fmt.Errorf("invalid function call arguments: %v", err).Error(), fmt.Errorf("invalid function call arguments: %v", err)
	}
	var usersPrompt, commandSummary string
	for _, message := range cc.Req.Messages {
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
		result, err := executeCommand(command)
		if err != nil {
			result = err.Error()
		}

		commandSummary = fmt.Sprintf("Prompt: %s\n\nCommand: %s\n\nResult: %s", usersPrompt, command, result)

	case "controlLights":
		light_name := params["light_name"].(string)
		state := params["state"].(bool)

		commandSummary = updateLight(light_name, state)
	}

	resultsummary, err := cc.summarizeResult(commandSummary)
	if err != nil {
		return err.Error(), err
	}

	return resultsummary, nil
}

// Add tool schema defintions
func (cc *ChatContext) addToolDefinitions(tool string) {

	// Define the function schema for executing commands
	toolReq := openai.ChatCompletionRequest{
		Model: cc.Req.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: `Use the provided tool defintion to answer the users prompt using the provided thought, and action context information.`,
			},
			{
				Role:    openai.ChatMessageRoleAssistant,
				Content: cc.Req.Messages[1].Content,
			},
			{
				Role:    openai.ChatMessageRoleAssistant,
				Content: cc.UserPrompt,
			},
		},
	}
	cc.Req = &toolReq

	switch tool {
	case "command":

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
		cc.Req.Tools = []openai.Tool{{
			Type:     openai.ToolTypeFunction,
			Function: &cmdFunc,
		}}

	case "lifx":
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
		lifxFunc := openai.FunctionDefinition{
			Name:        "controlLights",
			Description: "Control lifx lights with given parameters to turn them on or off",
			Parameters:  lifxParams,
		}
		cc.Req.Tools = []openai.Tool{{
			Type:     openai.ToolTypeFunction,
			Function: &lifxFunc,
		}}
	}
}

// Extract the summary portion into a separate function
func (cc *ChatContext) summarizeResult(result string) (string, error) {
	summaryReq := openai.ChatCompletionRequest{
		Model: cc.Req.Model,
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

	summaryStream, err := cc.Client.CreateChatCompletionStream(cc.Ctx, summaryReq)
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

func writeResponse(content string, pw *io.PipeWriter, req *openai.ChatCompletionRequest, resp *openai.ChatCompletionStreamResponse) {

	formattedResponse := openai.ChatCompletionStreamResponse{
		ID:                resp.ID,
		Object:            "chat.completion.chunk",
		Created:           resp.Created,
		Model:             req.Model,
		Choices:           []openai.ChatCompletionStreamChoice{{Index: 0, Delta: openai.ChatCompletionStreamChoiceDelta{Content: content}}},
		SystemFingerprint: resp.SystemFingerprint,
	}
	jsonResponse, err := json.Marshal(formattedResponse)
	if err != nil {
		pw.CloseWithError(err)
		return
	}
	prefixedResponse := fmt.Sprintf("data: %s\n", jsonResponse)
	if _, err := pw.Write([]byte(prefixedResponse)); err != nil {
		pw.CloseWithError(err)
		return
	}
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

func getLight(name string) *golifx.Bulb {
	bulbs, err := golifx.LookupBulbs()
	if err != nil {
		log.Fatalf("Error looking up bulbs: %v", err)
		return nil
	}

	bulbcount := len(bulbs)
	if bulbcount == 0 {
		log.Fatalf("%d bulbs found", bulbcount)
		return nil
	}

	for _, bulb := range bulbs {
		group, err := bulb.GetGroup()
		if err != nil {
			log.Printf("Error getting group for bulb %s: %v", group.Label, err)
			continue
		}
		if strings.EqualFold(group.Label, name) {
			return bulb
		}
	}
	return nil
}

func updateLight(light string, state bool) string {
	bulb := getLight(light)
	group, _ := bulb.GetGroup()
	if bulb != nil {
		bulb.SetPowerState(state)
		return fmt.Sprintf("%s light has been set to %t", group.Label, state)
	}
	return fmt.Sprintf("Unable to find %s", group)
}
