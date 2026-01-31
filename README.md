# VIBER (Versatile Intelligent Browser for Engineering Repositories)

VIBER is an intelligent CLI tool that transforms your local codebase
into an interactive AI assistant. By leveraging local LLMs via Ollama,
it allows you to have contextual conversations about your code,
generate documentation, explain complex logic, and get architectural  
 insights—all without sending your code to external APIs.

## ✨ Features

- **🔒 Privacy-First**: Uses local Ollama models; your code never
  leaves your machine
- **⚡ Concurrent Scanning**: Multi-worker file processing for large
  codebases
- **🎯 Smart Filtering**: Respects `.gitignore` patterns and filters
  by file extensions
- **🎨 Beautiful Output**: Renders AI responses with syntax
  highlighting and Markdown formatting

- **💬 Interactive REPL**: Chat session with context memory across
  questions
- **🚀 Zero Configuration**: Works out of the box with sensible
  defaults

      ## 📋 Prerequisites

      - [Ollama](https://ollama.com/) installed and running locally
      - A compatible model pulled (default: `kimi-k2.5:cloud`)
      - Go 1.21+ (if building from source)

      ## 🛠️ Installation

      ### From Source
      ```bash
      git clone https://github.com/yourusername/viber.git
      cd viber
      go build -o viber main.go
      sudo mv viber /usr/local/bin/

### Quick Start

    # In your project directory
    viber -dir .

## 🎯 Usage

### Basic Usage

    # Scan current directory
    viber

    # Scan specific directory
    viber -dir ./src

    # VIBER will load all relevant files and start an interactive session

### Interactive Commands

Once loaded, you can ask questions like:

• Explain the architecture of this codebase  
• What does the FileScanner struct do?  
• Generate unit tests for the AIClient  
• Find potential race conditions in the code  
• exit or quit to close the session

## ⚙️ Configuration

### Default Behavior

• Scanned Extensions: .go , .html (easily extensible in code)  
• Ignored Paths: .git , node_modules , and patterns from .gitignore  
• Workers: Uses all available CPU cores for scanning  
• Model: kimi-k2.5:cloud (configurable in source)

### Customizing File Types

Modify the main() function to scan different file types:

```go
scanner, _ := NewScanner(*dirPtr, ".gitignore", []string{".go", ".rs", ".ts", ".py"})
```

### Changing the AI Model

Edit the DEFAULT_MODEL constant or modify the AskAboutRepo method to
support model selection via flags.

## 🏗️ Architecture

VIBER consists of three main components:

1. FileScanner: Concurrent directory traversal with gitignore support
2. AIClient: Ollama API integration with streaming response handling
3. Context Builder: Aggregates file contents into a structured prompt
   for the LLM

The tool reads files concurrently, builds a codebase context string,
and maintains it in memory for the duration of the chat session.

## 🧪 Development

    # Run with verbose scanning
    go run main.go -dir ./test-project

    # Build for distribution
    GOOS=linux GOARCH=amd64 go build -o viber-linux main.go
    GOOS=darwin GOARCH=arm64 go build -o viber-macos main.go

## 🤝 Contributing

Contributions are welcome! Areas for improvement:

• Support for additional file extensions via CLI flags  
 • Configuration file support (YAML/TOML)  
 • Token counting and context window management  
 • Integration with other LLM providers (OpenAI, Anthropic)  
 • Export chat history to Markdown

## 📄 License

MIT License - see LICENSE file for details.

## 🙏 Acknowledgments

• Ollama https://github.com/ollama/ollama for local LLM inference  
 • Glamour https://github.com/charmbracelet/glamour for terminal
Markdown rendering  
 • Charmbracelet https://charm.sh/ for the beautiful CLI aesthetics
