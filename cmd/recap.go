package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const systemPromptGeneral = `You are a meeting summarizer. Create a concise, well-structured summary of the provided meeting transcript.

Cover:
- Main topics discussed
- Key decisions made
- Action items and next steps
- Important conclusions

Write the summary in the same language as the transcript.`

const systemPromptGrooming = `You are a sprint grooming meeting summarizer. Analyze the transcript and produce a structured summary focused on tasks and work items.

Use these sections (skip any that have no content):
1. **Tasks Discussed** — new stories, bugs, or work items identified; what needs to be done
2. **Priorities** — what is most urgent or important
3. **Watch Out For** — blockers, risks, edge cases, technical concerns raised
4. **Don't Forget** — dependencies, reminders, important details not to lose track of
5. **Assignments** — who should do what (only if explicitly mentioned)
6. **Sprint Goals** — high-level objectives discussed for the upcoming sprint

Write the summary in the same language as the transcript.`

const systemPromptPlanning = `You are a sprint planning meeting summarizer. Analyze the transcript and produce a structured summary focused on sprint commitment and task ownership.

Use these sections (skip any that have no content):
1. **Sprint Goals** — the main outcomes and objectives committed for this sprint
2. **Committed Tasks** — work items the team has taken into the sprint
3. **Assignments** — who owns which tasks
4. **Risks & Blockers** — potential issues, dependencies, or concerns raised
5. **Don't Forget** — critical details, edge cases, or follow-ups mentioned
6. **Decisions Made** — key decisions reached during the planning session

Write the summary in the same language as the transcript.`

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error,omitempty"`
}

// recapFromFile is called automatically after a recording session ends.
func recapFromFile(transcriptPath, meetingType, modelName, ollamaURL string) {
	content, err := os.ReadFile(transcriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recap: read transcript: %v\n", err)
		return
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		fmt.Println("recap: transcript is empty, skipping")
		return
	}
	fmt.Printf("\nGenerating %s recap using %s...\n", meetingType, modelName)
	recap, err := generateRecap(ollamaURL, modelName, pickSystemPrompt(meetingType), string(content))
	if err != nil {
		fmt.Fprintf(os.Stderr, "recap: %v\n", err)
		return
	}
	outPath, err := saveRecap(recap, transcriptPath, meetingType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recap: save: %v\n", err)
		return
	}
	fmt.Printf("\n--- RECAP ---\n%s\n\nSaved → %s\n", recap, outPath)
}

func runRecap(args []string) {
	fs := flag.NewFlagSet("recap", flag.ExitOnError)
	transcriptPath := fs.String("transcript", "", "path to transcript file (required)")
	meetingType := fs.String("type", "general", "meeting type: general, grooming, planning")
	modelName := fs.String("model", "gemma3:latest", "Ollama model name")
	ollamaURL := fs.String("ollama", "http://localhost:11434", "Ollama server base URL")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: lazy-listener recap -transcript <file> [-type general|grooming|planning] [-model <name>] [-ollama <url>]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *transcriptPath == "" {
		fmt.Fprintln(os.Stderr, "error: -transcript is required")
		fs.Usage()
		os.Exit(1)
	}

	content, err := os.ReadFile(*transcriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read transcript: %v\n", err)
		os.Exit(1)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		fmt.Fprintln(os.Stderr, "transcript is empty")
		os.Exit(1)
	}

	systemPrompt := pickSystemPrompt(*meetingType)

	fmt.Printf("Generating %s recap using %s...\n", *meetingType, *modelName)

	recap, err := generateRecap(*ollamaURL, *modelName, systemPrompt, string(content))
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate recap: %v\n", err)
		os.Exit(1)
	}

	outPath, err := saveRecap(recap, *transcriptPath, *meetingType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "save recap: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n--- RECAP ---\n%s\n\nSaved → %s\n", recap, outPath)
}

func pickSystemPrompt(meetingType string) string {
	switch strings.ToLower(meetingType) {
	case "grooming", "грумминг":
		return systemPromptGrooming
	case "planning", "планирование":
		return systemPromptPlanning
	default:
		return systemPromptGeneral
	}
}

func generateRecap(baseURL, model, systemPrompt, transcript string) (string, error) {
	reqBody := ollamaChatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "Here is the meeting transcript:\n\n" + transcript + "\n\nPlease generate the meeting summary."},
		},
		Stream: false,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("ollama request failed (is ollama running?): %w", err)
	}
	defer resp.Body.Close()

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("ollama: %s", result.Error)
	}
	return strings.TrimSpace(result.Message.Content), nil
}

func saveRecap(recap, transcriptPath, meetingType string) (string, error) {
	if err := os.MkdirAll("recaps", 0o755); err != nil {
		return "", err
	}
	base := strings.TrimSuffix(filepath.Base(transcriptPath), filepath.Ext(transcriptPath))
	base = strings.TrimPrefix(base, "transcript_")
	outPath := fmt.Sprintf("recaps/recap_%s_%s.md", base, strings.ToLower(meetingType))
	return outPath, os.WriteFile(outPath, []byte(recap+"\n"), 0o644)
}
