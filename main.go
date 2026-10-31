package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"workspace-manager/internal/app"
	"workspace-manager/internal/applications"
	"workspace-manager/internal/platform/linux"
	"workspace-manager/internal/projects"
	"workspace-manager/internal/projects/detection"
	"workspace-manager/internal/workspaces"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Profile editing runs locally. Process ownership will live
	// in the independent controller before any managed command execution is added.
	profiles := workspaces.NewService(linux.DesktopCatalog{})
	launcher := applications.NewLaunchService(linux.DesktopCatalog{},
		applications.WithCustomLauncher(linux.CustomLauncher{}, workspaces.NewCustomResolver(profiles)),
		applications.WithInstanceChecker(linux.DesktopInstances{}))
	var window *application.WebviewWindow
	projectService := projects.NewService(detection.LocalInspector{}, projects.WithDirectoryPicker(func() (string, error) {
		return application.Get().Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).
			CanCreateDirectories(false).SetTitle("Choose a project directory").AttachToWindow(window).PromptForSingleSelection()
	}))
	desktop := application.New(application.Options{
		Name:        "Workspace Manager",
		Description: "Local Linux developer workspaces",
		Services: []application.Service{
			application.NewService(&app.InfoService{}),
			application.NewService(profiles),
			application.NewService(applications.NewService(linux.DesktopCatalog{}, linux.DesktopIcons{OnMainThread: application.InvokeAsync})),
			application.NewService(launcher),
			application.NewService(projectService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	window = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Workspace Manager",
		Width:            1160,
		Height:           800,
		MinWidth:         760,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(16, 22, 23),
		URL:              "/",
	})

	if err := desktop.Run(); err != nil {
		log.Fatal(err)
	}
}
