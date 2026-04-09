package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/ollama/ollama/api"
)

const DEFAULT_MODEL = "qwen3.5:cloud"

// FileContent holds the metadata and actual text of the file
type FileContent struct {
	Path    string
	Content string
}

// Add this to your FileScanner
type FileIndex struct {
	Path    string
	Summary string // Optional: first 200 chars or function names
	Ext     string
}

// New method: Build index without loading full content
func (s *FileScanner) BuildIndex() ([]FileIndex, error) {
	var index []FileIndex
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || s.IgnoredNames[d.Name()] {
			return err
		}
		if !s.AllowedExts[filepath.Ext(path)] {
			return nil
		}
		// Read only first 500 bytes for summary
		f, _ := os.Open(path)
		buf := make([]byte, 500)
		f.Read(buf)
		index = append(index, FileIndex{
			Path:    path,
			Summary: string(buf),
			Ext:     filepath.Ext(path),
		})
		f.Close()
		return nil
	})
	return index, err
}

// FileScanner handles the directory traversal logic
type FileScanner struct {
	Root         string
	IgnoredNames map[string]bool
	Patterns     []string
	AllowedExts  map[string]bool
}

func NewScanner(root string, ignoreFile string, extensions []string) (*FileScanner, error) {
	s := &FileScanner{
		Root:         root,
		IgnoredNames: map[string]bool{".git": true, "node_modules": true},
		AllowedExts:  make(map[string]bool),
	}
	for _, ext := range extensions {
		s.AllowedExts[ext] = true
	}

	file, err := os.Open(ignoreFile)
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				s.Patterns = append(s.Patterns, line)
			}
		}
	}
	return s, nil
}

func (s *FileScanner) ScanForAI(workerCount int, callback func(fc FileContent)) error {
	pathsChan := make(chan string, 100)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range pathsChan {
				bytes, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				callback(FileContent{Path: path, Content: string(bytes)})
			}
		}()
	}

	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if s.IgnoredNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !s.AllowedExts[filepath.Ext(path)] {
			return nil
		}
		for _, p := range s.Patterns {
			if matched, _ := filepath.Match(p, d.Name()); matched {
				return nil
			}
		}
		pathsChan <- path
		return nil
	})

	close(pathsChan)
	wg.Wait()
	return err
}

// AIClient manages the connection to Ollama and Markdown rendering
type AIClient struct {
	client   *api.Client
	renderer *glamour.TermRenderer
}

func NewAIClient() (*AIClient, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, err
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	return &AIClient{client: client, renderer: r}, nil
}

// Spinner shows a small animation while the AI is thinking
func (ai *AIClient) playSpinner(ctx context.Context, done chan bool) {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	for {
		select {
		case <-done:
			fmt.Print("\r          \r") // Clear the spinner line
			return
		default:
			fmt.Printf("\r\033[35m%s\033[0m AI is thinking...", frames[i])
			i = (i + 1) % len(frames)
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (ai *AIClient) AskAboutRepo(ctx context.Context, repoContext string, userQuestion string) error {
	systemMsg := api.Message{
		Role:    "system",
		Content: "You are a Senior Software Engineer. Use the provided codebase to answer questions. Use Markdown for all formatting (code blocks, bold, headers).",
	}
	userMsg := api.Message{
		Role:    "user",
		Content: fmt.Sprintf("CODEBASE:\n%s\n\nQUESTION: %s", repoContext, userQuestion),
	}

	// Start the spinner in a background goroutine
	done := make(chan bool)
	go ai.playSpinner(ctx, done)

	var fullResponse strings.Builder
	req := &api.ChatRequest{
		Model:    DEFAULT_MODEL, // Change to your preferred local model
		Messages: []api.Message{systemMsg, userMsg},
		Stream:   new(bool), // Set to false to render full Markdown correctly
	}

	err := ai.client.Chat(ctx, req, func(res api.ChatResponse) error {
		fullResponse.WriteString(res.Message.Content)
		return nil
	})

	// Stop the spinner
	done <- true

	if err != nil {
		return err
	}

	// Render beautiful Markdown
	out, _ := ai.renderer.Render(fullResponse.String())
	fmt.Println(out)
	return nil
}

func (s *Session) AskQuestion(ctx context.Context, question string) error {
	// PHASE 1: Select
	fmt.Println("\033[90m🔍 Analyzing repository structure...\033[0m")
	relevantPaths, err := s.selectRelevantFiles(ctx, question)
	if err != nil {
		return err
	}

	// >>> REQUIREMENT: Return/List the files to the user
	if len(relevantPaths) > 0 {
		fmt.Println("\033[33m📄 Relevant Files Identified:\033[0m")
		for _, p := range relevantPaths {
			fmt.Printf("   - %s\n", p)
		}
	} else {
		fmt.Println("\033[33m📄 No specific files identified, using general context.\033[0m")
	}

	// PHASE 2: Load Content
	var builder strings.Builder
	for _, path := range relevantPaths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		builder.WriteString(fmt.Sprintf("\n--- FILE: %s ---\n%s\n", path, content))
	}

	// PHASE 3: Ask
	fmt.Println("\033[90m🤖 Generating answer...\033[0m")
	return s.ai.AskAboutRepo(ctx, builder.String(), question)
}

// Store index for later filtering
type Session struct {
	scanner *FileScanner
	index   []FileIndex
	ai      *AIClient
}

func (s *Session) selectRelevantFiles(ctx context.Context, question string) ([]string, error) {
	// 1. Prepare Index Context (Path + Summary)
	var indexContext strings.Builder
	for _, idx := range s.index {
		// Truncate summary to keep token count low during selection
		summary := idx.Summary
		if len(summary) > 100 {
			summary = summary[:100] + "..."
		}
		indexContext.WriteString(fmt.Sprintf("- %s (%s): %s\n", idx.Path, idx.Ext, summary))
	}

	// 2. Prompt specifically for JSON list of paths
	prompt := fmt.Sprintf(`Here is a list of files in the repository with summaries.
    Based on the question, return a JSON array of file paths that are relevant.
    Do not include any explanation, only the JSON array.

    INDEX:
    %s

    QUESTION: %s

    RESPONSE (JSON Array):`, indexContext.String(), question)

	// 3. Call LLM directly (bypass markdown renderer for parsing)
	req := &api.ChatRequest{
		Model: DEFAULT_MODEL,
		Messages: []api.Message{
			{Role: "system", Content: "You are a file selection engine. Return ONLY a JSON array of strings."},
			{Role: "user", Content: prompt},
		},
		Stream: new(bool), // False
	}

	var responseContent strings.Builder
	err := s.ai.client.Chat(ctx, req, func(res api.ChatResponse) error {
		responseContent.WriteString(res.Message.Content)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 4. Parse JSON result
	var paths []string
	// Clean up markdown code blocks if the model adds them despite instructions
	cleanJson := strings.ReplaceAll(responseContent.String(), "```json", "")
	cleanJson = strings.ReplaceAll(cleanJson, "```", "")

	err = json.Unmarshal([]byte(cleanJson), &paths)
	if err != nil {
		// Fallback: treat as newline separated if JSON fails
		paths = strings.Split(cleanJson, "\n")
	}

	// 5. Filter paths to ensure they exist in our index (Safety check)
	validPaths := make([]string, 0)
	indexMap := make(map[string]bool)
	for _, idx := range s.index {
		indexMap[idx.Path] = true
	}

	for _, p := range paths {
		p = strings.TrimSpace(p)
		if indexMap[p] {
			validPaths = append(validPaths, p)
		}
	}

	return validPaths, nil
}

func main() {
	dirPtr := flag.String("dir", ".", "The directory to analyze")
	flag.Parse()

	// 1. Initialize Components
	scanner, err := NewScanner(*dirPtr, ".gitignore", []string{".svelte", ".ts", ".go", ".html", ".sql"})
	if err != nil {
		fmt.Printf("Scanner Error: %v\n", err)
		return
	}

	ai, err := NewAIClient()
	if err != nil {
		fmt.Printf("AI Client Error: %v\n", err)
		return
	}

	// 2. Build Index (Lightweight)
	fmt.Printf("\033[36m📂 Building Index for %s...\033[0m\n", *dirPtr)
	index, err := scanner.BuildIndex()
	if err != nil {
		fmt.Printf("Index Error: %v\n", err)
		return
	}
	fmt.Printf("\033[32m✅ Indexed %d files\033[0m\n", len(index))

	// 3. Create Session (Wires Index + AI + Scanner)
	session := &Session{
		scanner: scanner,
		index:   index,
		ai:      ai,
	}

	// 4. Interactive Loop
	fmt.Println("\033[90mType 'exit' or 'quit' to close the session.\033[0m")
	inputScanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n\033[1;34m❯\033[0m ")
		if !inputScanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(inputScanner.Text())
		if userInput == "exit" || userInput == "quit" {
			break
		}
		if userInput == "" {
			continue
		}

		fmt.
			Println("\033[90m────────────────────────────────────────────────────────────\033[0m")

		// >>> USE SESSION FLOW INSTEAD OF RAW AI CALL
		if err := session.AskQuestion(context.Background(), userInput); err != nil {
			fmt.Printf("\033[31mAI Error: %v\033[0m\n", err)
		}

		fmt.
			Println("\033[90m────────────────────────────────────────────────────────────\033[0m")
	}
}
