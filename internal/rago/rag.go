package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/tmc/langchaingo/jsonschema"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
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

func LangChainQuery(prompt string) string {
	llm, err := openai.New(
		openai.WithModel("llama3-70b-8192"),
		openai.WithBaseURL("https://api.groq.com/openai/v1"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	resp, err := llm.GenerateContent(ctx,
		[]llms.MessageContent{
			// TODO add system prompts to further guide the response around output and command structure
			llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, prompt),
		},
		// llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
		// 	// fmt.Printf("Received chunk: %s\n", chunk)
		// 	return nil
		// }),
		llms.WithTools(tools))
	if err != nil {
		log.Fatal(err)
	}

	choice1 := resp.Choices[0]
	if choice1.FuncCall != nil {
		fmt.Printf("Function call: %v\n", choice1.FuncCall.Arguments)
		// Execute the function call
		var params map[string]interface{}
		err := json.Unmarshal([]byte(choice1.FuncCall.Arguments), &params)
		if err != nil {
			log.Fatal(err)
		}
		// Execute the function call
		result, err := executeCommand(params)
		if err != nil {
			log.Fatal("Failed to execute command: ", err.Error())
		}
		if result == "" {
			result = "command returned no result"
		}
		fmt.Printf("Command result: %s\n", result)

		// Send the result back to the AI for summarization
		summary, err := llm.GenerateContent(ctx,
			[]llms.MessageContent{
				llms.TextParts(llms.ChatMessageTypeSystem, "Keep the response short and do not repeat the question, just answer as required"),
				llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Provide very short summary for what happened based on the result of the following command execution:\nCommand:%s\nResult:%s", params["command"], result)),
			})
		if err != nil {
			log.Fatal(err)
		}
		// fmt.Printf("Summary: %s\n", summary.Choices[0].Content)

		return summary.Choices[0].Content + "\n"
	}
	return "Error failed to run command"
}

func executeCommand(params map[string]interface{}) (string, error) {
	command, ok := params["command"].(string)
	if !ok {
		return "", fmt.Errorf("command not specified or not a string")
	}

	_, ok = params["rationale"].(string)
	if !ok {
		return "", fmt.Errorf("rationale not specified or not a string")
	}

	parameters, ok := params["parameters"].(map[string]interface{})
	if !ok {
		parameters = map[string]interface{}{}
	}

	// Example handling for kubectl commands
	if strings.HasPrefix(command, "kubectl") {
		// Construct kubectl command
		cmd := exec.Command("kubectl", buildKubectlArgs(command, parameters)...)

		output, err := cmd.CombinedOutput()
		println("result:", string(output), err)
		if err != nil {
			return "", err
		}
		return string(output), nil
	}

	// Add more command handling logic as needed

	return "", fmt.Errorf("unsupported command")
}

func buildKubectlArgs(command string, params map[string]interface{}) []string {
	args := strings.Split(command, " ")[1:]
	for key, value := range params {
		args = append(args, key, fmt.Sprintf("%v", value))
	}
	return args
}

var tools = []llms.Tool{
	{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "executeCommand",
			Description: "Execute a command with given parameters",
			Parameters: jsonschema.Definition{
				Type: jsonschema.Object,
				Properties: map[string]jsonschema.Definition{
					"rationale": {
						Type:        jsonschema.String,
						Description: "The rationale for choosing this function call with these parameters",
					},
					"command": {
						Type:        jsonschema.String,
						Description: "The command to be executed, e.g. 'kubectl get pods'",
					},
					// "parameters": {
					// 	Type:        jsonschema.Object,
					// 	Description: "Additional parameters for the command as key-value pairs",
					// },
				},
				Required: []string{"rationale", "command"},
			},
		},
	},
}
