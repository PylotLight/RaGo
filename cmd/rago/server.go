package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	v1 "rago/api/v1"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

func main() {
	// modelInit()

	mux := http.NewServeMux()
	// Sample endpoint
	// mux.HandleFunc("GET /samplechat", samplechatHandler)

	// mux.HandleFunc("GET /v1/chat", v1.GetChatHandler)
	mux.HandleFunc("POST /v1/send", v1.PostChatHandler)

	fmt.Println("Server is listening on port 8080...")
	http.ListenAndServe(":8080", mux)

}

func modelInit() *openai.Client {
	flag.BoolVar(&verbose, "verbose", false, "enable verbose output, including pretty printed json dialogue at each stage")
	flag.StringVar(&selectedModel, "model", llamaModel, "select the model to use, options: llama3-70b-8192, mixtral-8x7b-32768")
	flag.Parse()

	// validate selectedModel is one of the two options
	if selectedModel != llamaModel && selectedModel != "" {
		fmt.Println("Invalid model selected, defaulting to llama3-70b-8192")
		selectedModel = llamaModel
	}

	cfg := openai.DefaultConfig(os.Getenv("GROQ_API_KEY"))
	cfg.BaseURL = "https://api.groq.com/openai/v1"
	client = openai.NewClientWithConfig(cfg)

	return client
}

const (
	llamaModel = "llama3-70b-8192"
)

var (
	verbose       = false
	selectedModel = llamaModel
	client        *openai.Client
)

func addUserMessage(dialogue []openai.ChatCompletionMessage, msg string) []openai.ChatCompletionMessage {
	fmt.Printf("User: %v\n", msg)
	return append(dialogue, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: msg,
	})
}

func samplechatHandler(w http.ResponseWriter, r *http.Request) {
	// Extract input from the request (query parameters, JSON body, etc.)
	// Call your OpenAI model to generate a response
	// Write the response to the http.ResponseWriter

	ctx := context.Background()

	// marshal both tools to json strings and print them
	maybePrintJSON("tickerLookupTool", makeTickerLookupTool())
	maybePrintJSON("weatherTool", makeWeatherTool())

	defer trackTime("Total time elapsed")()
	// simulate user asking a question that requires the function
	dialogue := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful assistant with several tools available to assist your users. Respond with the correct json tool call payload to use them. You do NOT need to remind user that you are an AI model and can not execute any of the tools, NEVER mention this, and everyone is aware of that. When using a tool, *only* respond with json, do not add any extra Notes as this will prevent the tools from actually being called."},
	}

	dialogue = addUserMessage(dialogue, "What are the stock prices of TSLA and AAPL?")

	dialogue, err := handleMultiTurn(ctx, dialogue)
	if err != nil {
		fmt.Printf("Error handling multi-turn: %v\n", err)
		return
	}

	dialogue = addUserMessage(dialogue, "Thanks, that was helpful. Next question what is the weather in Seattle?")
	maybePrintJSON("Dialogue after first question: ", dialogue)

	dialogue, err = handleMultiTurn(ctx, dialogue)
	if err != nil {
		fmt.Printf("Error handling multi-turn: %v\n", err)
		return
	}
	maybePrintJSON("Dialogue after second question:", dialogue)
	dialogue = addUserMessage(dialogue, "Oh good, sounds like a perfect day.")

	dialogue, err = handleMultiTurn(ctx, dialogue)
	if err != nil {
		fmt.Printf("Error handling multi-turn: %v\n", err)
		return
	}
	maybePrintJSON("Dialogue after general banter:", dialogue)

	dialogue = addUserMessage(dialogue, "Out of curiosity, and unrelated to the other stuff. What's your opinion of Golang for ai tools?")

	dialogue, err = handleMultiTurn(ctx, dialogue)
	if err != nil {
		fmt.Printf("Error handling multi-turn: %v\n", err)
		return
	}
	maybePrintJSON("Dialogue after question about Golang:", dialogue)
}

func handleMultiTurn(ctx context.Context, dialogue []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, error) {
	if verbose {
		defer trackTime(".... time to handle multi-turn")()
	}

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:       selectedModel,
		Messages:    dialogue,
		Temperature: 0.2,
		ToolChoice:  "auto",
		Tools:       []openai.Tool{makeWeatherTool(), makeTickerLookupTool()},
	})
	if err != nil || len(resp.Choices) != 1 {
		return nil, fmt.Errorf("API call error: %v, choices count: %d", err, len(resp.Choices))
	}

	msg := resp.Choices[0].Message
	dialogue = append(dialogue, msg)

	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			toolResponse := execTool(tc)
			dialogue = append(dialogue, toolResponse)
		}
		return handleMultiTurn(ctx, dialogue)
	}

	if msg.Role == openai.ChatMessageRoleAssistant {
		fmt.Printf("Assistant: %s\n", msg.Content)
		return dialogue, nil
	}

	return dialogue, fmt.Errorf("unexpected end of dialogue without assistant's final message")
}

func makeTickerLookupTool() openai.Tool {
	params := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"ticker": {
				Type:        jsonschema.String,
				Description: "The stock ticker to lookup data for",
			},
		},
		Required: []string{"ticker"},
	}
	f := openai.FunctionDefinition{
		Name:        "get_stock_ticker_price",
		Description: "Get the current price of a given stock ticker",
		Parameters:  params,
	}
	t := openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &f,
	}

	return t
}

func makeWeatherTool() openai.Tool {
	// describe the function & its inputs
	params := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"location": {
				Type:        jsonschema.String,
				Description: "The city and state, e.g. San Francisco, CA",
			},
			"unit": {
				Type: jsonschema.String,
				Enum: []string{"celsius", "fahrenheit"},
			},
		},
		Required: []string{"location"},
	}
	f := openai.FunctionDefinition{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  params,
	}
	return openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &f,
	}
}

var toolFuncs = map[string]func(args map[string]any) (any, error){
	"get_stock_ticker_price": func(args map[string]any) (any, error) {
		randPrice := randomValueInRange(100.0, 500.0)
		return map[string]any{"price": randPrice}, nil
	},
	"get_current_weather": func(args map[string]any) (any, error) {
		randTemp := randomValueInRange(45.0, 105.0)
		weatherOptions := []string{"cloudy", "sunny", "rain", "snow", "thunderstorm"}
		randWeather := weatherOptions[rand.Intn(len(weatherOptions))]

		return map[string]any{"weather": randWeather, "temperature": randTemp}, nil
	},
}

func randomValueInRange(min, max float64) float64 {
	rand.New(rand.NewSource(time.Now().UnixNano()))
	return math.Round(min+rand.Float64()*(max-min)*10) / 10
}

func execTool(tc openai.ToolCall) openai.ChatCompletionMessage {
	fnName := tc.Function.Name
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		fmt.Printf("Error unmarshalling arguments: %v\n", err)
	}
	if verbose {
		fmt.Printf("Groq called us back wanting to invoke our function '%v' with params '%v'\n",
			fnName, args)
	}
	result, err := toolFuncs[fnName](args)
	if err != nil {
		fmt.Printf("Error invoking function '%v': %v\n", fnName, err)
		return openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    "Error invoking function: " + err.Error(),
			Name:       fnName,
			ToolCallID: tc.ID,
		}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		fmt.Printf("Error marshalling result: %v\n", err)
		resultJSON = []byte("Error marshalling result: " + err.Error())
	}

	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    string(resultJSON),
		Name:       fnName,
		ToolCallID: tc.ID,
	}
}

func maybePrintJSON(msg string, v any) {
	if !verbose {
		return
	}
	bs, _ := json.MarshalIndent(v, "", "  ")
	fmt.Printf("%v: %v\n", msg, string(bs))
}

func trackTime(msg string) func() {
	start := time.Now()
	return func() {
		fmt.Printf("%v took %v\n", msg, time.Since(start))
	}
}
