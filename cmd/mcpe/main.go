package main

import (
	"context"
	"os"

	"github.com/Melon-cream/mcp-estuary/internal/app"
)

func main() {
	code := app.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
