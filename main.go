package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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

func main() {
	dirPtr := flag.String("dir", ".", "The directory to analyze")
	flag.Parse()

	// Initialization
	scanner, _ := NewScanner(*dirPtr, ".gitignore", []string{".svelte", ".ts", ".go", ".html", ".sql"})
	ai, err := NewAIClient()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// 1. Scan Files
	start := time.Now()
	var fileCount int64
	var mu sync.Mutex
	var builder strings.Builder

	fmt.Printf("\033[36m📂 Scanning %s...\033[0m\n", *dirPtr)

	process := func(fc FileContent) {
		atomic.AddInt64(&fileCount, 1)
		mu.Lock()
		builder.WriteString(fmt.Sprintf("\n--- FILE: %s ---\n%s\n", fc.Path, fc.Content))
		mu.Unlock()
	}

	_ = scanner.ScanForAI(runtime.NumCPU(), process)
	repoContext := builder.String()

	fmt.Printf("\033[32m✅ %d files loaded into context (%v)\033[0m\n", fileCount, time.Since(start))
	fmt.Println("\033[90mType 'exit' or 'quit' to close the session.\033[0m")

	// 2. Interactive Loop
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

		fmt.Println("\033[90m────────────────────────────────────────────────────────────\033[0m")
		if err := ai.AskAboutRepo(context.Background(), repoContext, userInput); err != nil {
			fmt.Printf("\033[31mAI Error: %v\033[0m\n", err)
		}
		fmt.Println("\033[90m────────────────────────────────────────────────────────────\033[0m")
	}
}
