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

		// addToolDefinitions(&req)
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
	You are a Question Answering AI with reasoning ability and the capability to execute commands via tools.
	You will receive a Question from the User.
	In order to answer any Question, you must follow a strict loop of Thought, Verification, Action, PAUSE, Observation.

	1. Use "Thought: " to describe your initial thoughts about the question being asked and to determine if an action is needed.
	2. Use "Verification: " to explicitly confirm if an action is necessary based on the current information and context. If an action is not necessary, proceed to provide the answer directly.
	3. Use "Action: " to define and run one of the actions available to you if required - then return the suggested action e.g command/lifx: then return PAUSE. NEVER continue generating "Observation: " or "Answer: " in the same response that contains PAUSE.
	4. "Observation" will be presented to you as the result of the previous "Action".
	If from the Thought or Observation you can derive the answer to the Question, you MUST output "Answer: ", followed by the answer and the answer ONLY, without explanation of the steps used to arrive at the answer.

	If the "Observation" you received is not related to the question asked, or you cannot derive the answer from the observation, change the Action to be performed and try again.

	If the Question or action ever requires a specific pod name, use the Pod search formula to pull and match the correct pod name.

	Your 3 available "Actions" are:
	- Command: Execute a Kubernetes or linux system command (e.g., kubectl get pods, free -h, grep, awk '{print $1}')
	- Lifx: Control a smart light (e.g., bedroom off)
	- Search: Run a google search to pull relevant results from the internet

	Examples:
	Question: Can you delete the jellyfin pod?
	Thought: I need to find the exact name of the jellyfin pod first by using the "Pod search formula" with the Command: action.
	Verification: Deleting a pod requires an exact name, confirming the need for an action.
	Action: echo $(kubectl get pods --no-headers=true | grep jellyfin | awk '{print $1}' | head -1)
	Observation: jellyfin-xyz123
	Thought: I found the pod matching the search prompt "jellyfin". Now I can delete it using the Command: action.
	Action: kubectl delete pod jellyfin-xyz123

	Question: Can you summarize the best Intel/AMD CPUs for an efficient NAS build?
	Thought: The question requires a summary of CPU choices for a NAS build, which is a knowledge-based answer.
	Verification: No action is needed. I should provide the answer directly.
	Answer: <Generated Relevant Answer>
	`
		// Add system prompt
		cc.Req.Messages = append([]openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: reActPrompt,
			},
		}, cc.Req.Messages...)

		cc.Process_ChatStream()
	}()

	return cc.PReader, nil
}
