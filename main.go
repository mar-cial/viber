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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/ollama/ollama/api"
)

const DEFAULT_MODEL = "gemma4:31b-cloud"
const BIG_MODEL = "deepseek-v4-pro:cloud"

const CONFIG_DIR = ".ollama-interactive"
const CONFIG_FILE = "config.json"

// Config stores user preferences
type Config struct {
	DefaultModel string `json:"default_model"`
	LastUsed     string `json:"last_used"`
}

func GetConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configDir := filepath.Join(home, ".config", CONFIG_DIR)

	// Create directory if not exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}

	return filepath.Join(configDir, CONFIG_FILE), nil
}

// LoadConfig loads the configuration from file
func LoadConfig() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{DefaultModel: DEFAULT_MODEL}, nil
		}
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return &Config{DefaultModel: DEFAULT_MODEL}, nil
	}

	if config.DefaultModel == "" {
		config.DefaultModel = DEFAULT_MODEL
	}

	return &config, nil
}

// SaveConfig saves the configuration to file
func SaveConfig(config *Config) error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// ListModels fetches available models from Ollama
func ListModels(client *api.Client) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.List(ctx)
	if err != nil {
		return nil, err
	}

	var models []string
	for _, model := range resp.Models {
		models = append(models, model.Name)
	}

	return models, nil
}

// SelectModel shows interactive model selection
func SelectModel(models []string, defaultModel string) (string, error) {
	fmt.Println("\033[36m📋 Modelos Disponibles:\033[0m")

	for i, m := range models {
		fmt.Printf("   \033[90m[%d]\033[0m %s", i+1, m)
		if m == defaultModel {
			fmt.Printf(" \033[32m(default)\033[0m")
		}
		fmt.Println()
	}

	fmt.Println("\n\033[90mPresiona Enter para usar el default, o escribe el número del modelo:\033[0m")
	fmt.Print("\033[1;34m❯\033[0m ")

	inputScanner := bufio.NewScanner(os.Stdin)
	if !inputScanner.Scan() {
		return defaultModel, nil
	}

	input := strings.TrimSpace(inputScanner.Text())

	// Empty input = use default
	if input == "" {
		return defaultModel, nil
	}

	// Parse number
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(models) {
		fmt.Printf("\033[31m❌ Selección inválida, usando default: %s\033[0m\n", defaultModel)
		return defaultModel, nil
	}

	selected := models[idx-1]
	fmt.Printf("\033[32m✅ Modelo seleccionado: %s\033[0m\n", selected)
	return selected, nil
}

type AIClient struct {
	client   *api.Client
	renderer *glamour.TermRenderer
	Model    string // ← Agregar campo para el modelo seleccionado
}

// Agrega esto en Session para permitir cambiar modelo
func (s *Session) ChangeModel(newModel string) {
	s.ai.UpdateModel(newModel)
	fmt.Printf("\033[32m✅ Modelo cambiado a: %s\033[0m\n", newModel)
}

func NewAIClient(model string) (*AIClient, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, err
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	return &AIClient{
		client:   client,
		renderer: r,
		Model:    model, // ← Usar modelo pasado como parámetro
	}, nil
}

// UpdateModel allows changing the model during session
func (ai *AIClient) UpdateModel(model string) {
	ai.Model = model
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

	done := make(chan bool)
	go ai.playSpinner(ctx, done)

	var fullResponse strings.Builder
	req := &api.ChatRequest{
		Model:    ai.Model, // ← Usar el modelo almacenado en la instancia
		Messages: []api.Message{systemMsg, userMsg},
		Stream:   new(bool),
	}

	err := ai.client.Chat(ctx, req, func(res api.ChatResponse) error {
		fullResponse.WriteString(res.Message.Content)
		return nil
	})

	done <- true

	if err != nil {
		return err
	}

	out, _ := ai.renderer.Render(fullResponse.String())
	fmt.Println(out)
	return nil
}

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

func (s *FileScanner) BuildIndex() ([]FileIndex, error) {
	var index []FileIndex
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// ✅ Skip ignored directories (prevents walking into them)
		if d.IsDir() {
			if s.IgnoredNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip disallowed extensions
		if !s.AllowedExts[filepath.Ext(path)] {
			return nil
		}

		// Check .gitignore patterns
		for _, p := range s.Patterns {
			if matched, _ := filepath.Match(p, d.Name()); matched {
				return nil
			}
		}

		// Read only first 500 bytes for summary
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		buf := make([]byte, 500)
		f.Read(buf)
		index = append(index, FileIndex{
			Path:    path,
			Summary: string(buf),
			Ext:     filepath.Ext(path),
		})

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
		Root: root,
		IgnoredNames: map[string]bool{
			".git":         true,
			"node_modules": true,
			".svelte-kit":  true,
			"build":        true,
			"dist":         true,
			".vercel":      true,
			".next":        true,
			"__pycache__":  true,
			"vendor":       true,
		},
		AllowedExts: make(map[string]bool),
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

	allowedExtensions := []string{".svelte", ".ts", ".go", ".html", ".sql", ".yml", "justfile", ".rs"}

	// 1. Cargar configuración
	fmt.Println("\033[36m🔧 Cargando configuración...\033[0m")
	config, err := LoadConfig()
	if err != nil {
		fmt.Printf("\033[33m⚠️  Error cargando config, usando defaults\033[0m\n")
		config = &Config{DefaultModel: DEFAULT_MODEL}
	}

	// 2. Inicializar cliente AI temporal para listar modelos
	tempAI, err := NewAIClient(config.DefaultModel)
	if err != nil {
		fmt.Printf("AI Client Error: %v\n", err)
		return
	}

	fmt.Println("\033[36m🔍 Conectando con Ollama...\033[0m")
	models, err := ListModels(tempAI.client)
	if err != nil {
		// ... existing error handling ...
	}

	// >>> NEW: Calculate defaultIdx in main
	defaultIdx := -1
	for i, m := range models {
		if m == config.DefaultModel {
			defaultIdx = i
			break
		}
	}

	if defaultIdx == -1 {
		fmt.Printf("\033[33m⚠️  Default model '%s' not found in local models.\033[0m\n", config.
			DefaultModel)
	} else {
		fmt.Printf("\033[32m✅ Default model found at index %d\033[0m\n", defaultIdx)
	}

	// 4. Selección de modelo (si hay más de uno)
	selectedModel := config.DefaultModel
	if len(models) > 1 {
		selectedModel, err = SelectModel(models, config.DefaultModel)
		if err != nil {
			fmt.Printf("\033[33m⚠️  Error en selección, usando default\033[0m\n")
			selectedModel = config.DefaultModel
		}

		// Preguntar si quiere guardar como default
		if selectedModel != config.DefaultModel {
			fmt.Print("\033[90m¿Guardar como modelo por defecto? (y/n): \033[0m")
			inputScanner := bufio.NewScanner(os.Stdin)
			if inputScanner.Scan() {
				if strings.ToLower(strings.TrimSpace(inputScanner.Text())) == "y" {
					config.DefaultModel = selectedModel
					if err := SaveConfig(config); err != nil {
						fmt.Printf("\033[33m⚠️  No se pudo guardar la configuración\033[0m\n")
					} else {
						fmt.Printf("\033[32m✅ Configuración guardada en ~/.config/%s/\033[0m\n", CONFIG_DIR)
					}
				}
			}
		}
	}

	// 5. Inicializar componentes con modelo seleccionado
	scanner, err := NewScanner(*dirPtr, ".gitignore", allowedExtensions)
	if err != nil {
		fmt.Printf("Scanner Error: %v\n", err)
		return
	}

	ai, err := NewAIClient(selectedModel) // ← Usar modelo seleccionado
	if err != nil {
		fmt.Printf("AI Client Error: %v\n", err)
		return
	}

	// 6. Build Index
	fmt.Printf("\033[36m📂 Building Index for %s...\033[0m\n", *dirPtr)
	index, err := scanner.BuildIndex()
	if err != nil {
		fmt.Printf("Index Error: %v\n", err)
		return
	}
	fmt.Printf("\033[32m✅ Indexed %d files\033[0m\n", len(index))

	// 7. Create Session
	session := &Session{
		scanner: scanner,
		index:   index,
		ai:      ai,
	}

	// 8. Interactive Loop
	fmt.Println("\033[90mType 'exit' or 'quit' to close the session.\033[0m")
	fmt.Println("\033[90mType 'model' to change the current model.\033[0m")

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

		// ← Comando para cambiar modelo
		if userInput == "model" {
			newModel, err := SelectModel(models, session.ai.Model)
			if err == nil && newModel != "" {
				session.ChangeModel(newModel)
				config.DefaultModel = newModel
				SaveConfig(config)
			}
			continue
		}

		if userInput == "" {
			continue
		}

		fmt.Println("\033[90m────────────────────────────────────────────────────────────\033[0m")

		if err := session.AskQuestion(context.Background(), userInput); err != nil {
			fmt.Printf("\033[31mAI Error: %v\033[0m\n", err)
		}

		fmt.Println("\033[90m────────────────────────────────────────────────────────────\033[0m")
	}
}
