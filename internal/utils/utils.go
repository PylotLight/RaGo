package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"

	"github.com/2tvenom/golifx"
	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

type ChatContext struct {
	Client  *openai.Client
	Ctx     context.Context
	PWriter *io.PipeWriter
	PReader *io.PipeReader
	Req     *openai.ChatCompletionRequest
	Resp    *openai.ChatCompletionStreamResponse
}

func (cc *ChatContext) Process_ChatStream() {
	stream, err := cc.Client.CreateChatCompletionStream(cc.Ctx, *cc.Req)
	if err != nil {
		return
	}
	defer stream.Close()
	for {
		resp, err := stream.Recv()
		cc.Resp = &resp
		if err == io.EOF {
			// Write the [DONE] message to indicate end of stream
			if _, err := cc.PWriter.Write([]byte("data: [DONE]\n")); err != nil {
				cc.PWriter.CloseWithError(err)
				return
			}
			break
		}
		if err != nil {
			cc.PWriter.CloseWithError(err)
			return
		}
		if err := cc.chat_loop(); err != nil {
			cc.PWriter.CloseWithError(err)
			return
		}
	}
}

func (cc *ChatContext) chat_loop() error {
	var result string

	for _, choice := range cc.Resp.Choices {
		switch len(choice.Delta.ToolCalls) {
		case 1:
			fmt.Printf("%+v\n", cc.Resp.Choices)
			// Tool choice is made and executed returning the result
			tool_result, err := cc.handle_ToolCall(&choice.Delta.ToolCalls[0])
			if err != nil {
				cc.PWriter.CloseWithError(err)
				return err
			}
			result = tool_result
		default:
			// fmt.Printf("%+v\n", resp.Choices)
			result = choice.Delta.Content
			println(result)
			if strings.Contains(result, "PAUSE") {
				switch result {
				case "command":
					cc.addToolDefinitions("command")
				case "lifx":
					cc.addToolDefinitions("lifx")
				}
				// Create another chat stream to evaluate the planned action before the PAUSE and execute if accurate.
				// Call the OpenAI API with streaming
				cc.Process_ChatStream()
				writeResponse(cc.Resp.Choices[0].Delta.Content, cc.PWriter, cc.Req, cc.Resp)
			}
			if strings.EqualFold(result, "action:") {
				println("action!!", result)
			}
		}
		writeResponse(result, cc.PWriter, cc.Req, cc.Resp)
	}
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
		println("command:", command)
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

	// println(commandSummary)
	resultsummary, err := cc.summarizeResult(commandSummary)
	if err != nil {
		return err.Error(), err
	}

	return resultsummary, nil
}

// Add tool schema defintions
func (cc *ChatContext) addToolDefinitions(tool string) {
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
	switch tool {
	case "command":
		cc.Req.Tools = []openai.Tool{{
			Type:     openai.ToolTypeFunction,
			Function: &cmdFunc,
		}}
	case "lifx":
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
