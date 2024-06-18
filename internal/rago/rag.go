package rag

import (
	"context"
	"io"
	"log"

	"rago/internal/utils"

	"github.com/sashabaranov/go-openai"
)

type ChatContext struct {
	Client  *openai.Client
	Ctx     context.Context
	PWriter *io.PipeWriter
	PReader *io.PipeReader
	Resp    *openai.ChatCompletionStreamResponse
	Req     *openai.ChatCompletionRequest
}

// Modified GenerateCompletion function with refactored logic
func GenerateCompletion(req *openai.ChatCompletionRequest, token string) (io.Reader, error) {
	config := openai.DefaultConfig(token)
	config.BaseURL = "https://api.groq.com/openai/v1"
	c := openai.NewClientWithConfig(config)
	ctx := context.Background()

	// Create a pipe to stream the response
	pr, pw := io.Pipe()

	cc := &utils.ChatContext{
		Client:  c,
		Ctx:     ctx,
		PWriter: pw,
		PReader: pr,
		Req:     req,
	}

	go func() {
		// defer pw.Close()
		defer func() {
			if err := cc.PWriter.Close(); err != nil {
				log.Printf("Error closing pipe writer: %v", err)
			}
		}()
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
	
		Ensure the output is correct and complete. If additional information is needed, end the session with PAUSE to allow another prompt to gather required details externally.
	
		`
		/*
			For multi-step processes, think through the sequence of commands needed to achieve the final goal and execute them accordingly. For example, if a specific pod's logs are requested but not provided, first list the pods using '$(kubectl get pods | grep app | awk '{print $1}' | head -1)' to find the relevant name, then retrieve the logs for that pod.
			When a specific pod name is needed, use multiple inline commands, ensure they are correctly formatted. Examples:
			- kubectl describe pod $(kubectl get pods --no-headers=true | grep app | awk '{print $1}' | head -1)
			- kubectl logs $(kubectl get pods | grep app | awk '{print $1}' | head -1) | tail -50
			- free -h | awk '{print $1, $2, $3}' */
		const reActPrompt = `
	You are a Question Answering AI with reasoning ability and the capability to execute commands via tools.
	You will receive a Question from the User.
	In order to answer any Question, you must follow a strict loop of Thought, Action, PAUSE, Observation.

	1. Use "Thought: " to describe your initial thoughts about the question being asked and to determine if an action is needed.
	2. Use "Action: " to define and run one of the actions available to you by returning the suggested action e.g Command/lifx explicitly by action name, then ALWAYS end with PAUSE. NEVER continue generating "Observation: " or "Answer: " in the same response that contains PAUSE.
	3. "Observation" will be presented to you as the result of the previous "Action".
	If from the Thought or Observation you can derive the answer to the Question, you MUST output "Answer: ", followed by the answer and the answer ONLY, without explanation of the steps used to arrive at the answer.

	If the Question or action ever requires a specific pod name, use the Pod search formula to pull and match the correct pod name.

	Your 3 available "Actions" are:
	- Command: Execute a Kubernetes or linux system command (e.g., kubectl get pods, free -h, grep, awk '{print $1}')
	- Lifx: Control a smart light (e.g., bedroom off)
	- Search: Run a google search to pull relevant results from the internet

	Examples:
	Question: Can you delete the jellyfin pod?
	Thought: I need to find the exact name of the jellyfin pod first by using the "Pod search formula" with the Command[] action
	Action: Command[echo $(kubectl get pods --no-headers=true | grep jellyfin | awk '{print $1}' | head -1)]
	Observation: jellyfin-xyz123
	Thought: I found the pod matching the search prompt "jellyfin" which can be deleted with the following action Command[kubectl delete pod jellyfin-xyz123]
	Action: Command[Generated command]

	Question: Can you give a 1 sentence explanation of AI transformers?
	Thought: The question requires a short explanation of AI transformers, which is a knowledge-based answer.
	Answer: <Generated Relevant Answer>

	Question: What's the latest Go version?
	Thought: The question requires a search query on reliable online sources, such as the official Go documentation or trusted package repositories, 
	to retrieve the latest version of Go and ensure I have the most up-to-date information via the Search action.
	Action: Search[Latest Go version including minor versions]
	Observation: The latest major version of Go is 1.22 with latest minor version being 1.22.4
	Answer: As of now, the latest major version of Go is 1.22, and the latest minor version is 1.22.4.
	`
		// Add system prompt
		cc.Req.Messages = append([]openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: reActPrompt,
			},
		}, cc.Req.Messages...)
		// Either include tool defs here or not..?
		// cc.addToolDefinitions(&req)
		cc.Process_ChatStream()
	}()

	return cc.PReader, nil
}
