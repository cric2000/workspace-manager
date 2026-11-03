//go:build !mcp

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func smokeServices(func() *application.WebviewWindow) []application.Service { return nil }
