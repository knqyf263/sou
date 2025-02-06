# sou

A modern TUI tool for exploring container image layers with an intuitive interface.

> The name "sou" comes from the Japanese word "層" (そう/sō) which means "layer". It can be pronounced as "so".

![Demo](docs/demo.gif)

## Features

- 🚀 Interactive TUI for seamless navigation through container images
- 🔍 Explore files within each layer using a built-in file picker
- 👀 Quick preview of file contents within layers
- 💾 Easy export of files from layers to your local filesystem
- 📄 View image manifests and configurations
- 📦 Support for both local and remote container images

## Note

> 🤖 This project is experimentally developed entirely with Claude 3.5 Sonnet, exploring the possibilities of AI-assisted development.

## Installation

### Using Homebrew

```bash
brew install knqyf263/sou/sou
```

### Using Container Image

```bash
docker run --rm -it ghcr.io/knqyf263/sou:latest nginx:latest
```

### Using Go

```bash
go install github.com/knqyf263/sou@latest
```

### From Source

```bash
git clone https://github.com/knqyf263/sou.git
cd sou
go build -o sou
```

## Usage

```bash
sou <image-name>
```

Example:
```bash
# Local image
sou nginx:latest

# Remote image
sou ghcr.io/knqyf263/my-image:latest
```

## Key Bindings

### Layer View
- `↑/k`: Move cursor up
- `↓/j`: Move cursor down
- `→/l`: View layer contents
- `g`: Go to first item
- `G`: Go to last item
- `K/pgup`: Page up
- `J/pgdown`: Page down
- `yy`: Copy layer diff ID
- `/`: Filter layers
- `?`: Toggle help
- `q`: Quit

### File View
- `↑/k`: Move cursor up
- `↓/j`: Move cursor down
- `←/h`: Go back
- `→/l`: View/open file
- `.`: Toggle hidden files
- `x`: Export file
- `/`: Filter files
- `?`: Toggle help
- `q`: Quit

### File Content View
- `↑/k`: Scroll up
- `↓/j`: Scroll down
- `←/h`: Go back to file list
- `q`: Quit

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for more details.