package main

import (
	"context"
	"embed"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
	"workspace-manager/internal/app"
	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/lifecycle"
	"workspace-manager/internal/platform/linux"
	"workspace-manager/internal/projects"
	"workspace-manager/internal/projects/detection"
	"workspace-manager/internal/storage"
	"workspace-manager/internal/workspaces"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if len(os.Args) > 1 {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		paths, err := storage.ResolvePaths()
		if err != nil {
			log.Fatal(err)
		}
		switch os.Args[1] {
		case "--controller":
			executable, err := os.Executable()
			if err != nil {
				log.Fatal(err)
			}
			controller, err := lifecycle.NewController(paths, linux.LifecycleHooks(), executable)
			if err != nil {
				log.Fatal(err)
			}
			defer controller.Close()
			if err := lifecycle.Serve(ctx, controller); err != nil {
				log.Fatal(err)
			}
			return
		case "--task-worker":
			// The worker handles graceful/force signals itself after registration.
			cancel()
			if len(os.Args) != 3 {
				log.Fatal("a run ID is required")
			}
			if err := lifecycle.RunWorker(context.Background(), paths, domain.ID(os.Args[2]), linux.ReviewAction); err != nil {
				log.Fatal(err)
			}
			return
		case "--login":
			result := lifecycle.NewService().Execute(ctx, lifecycle.Request{Action: "login"})
			if result.Code != "" {
				log.Print(result.Message)
				os.Exit(1)
			}
			return
		case "--status":
			result := lifecycle.NewService().Execute(ctx, lifecycle.Request{Action: "state"})
			if result.Code != "" {
				log.Print(result.Message)
				os.Exit(1)
			}
			_ = json.NewEncoder(os.Stdout).Encode(result)
			return
		case "--stop-controller":
			result := lifecycle.NewService().Execute(ctx, lifecycle.Request{Action: "stop_controller"})
			if result.Code != "" {
				log.Print(result.Message)
				os.Exit(1)
			}
			return
		default:
			log.Fatal("unknown mode; use --controller, --login, --status or --stop-controller")
		}
	}
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
		Linux:       application.LinuxOptions{ApplicationID: "io.github.workspace_manager.WorkspaceManager"},
		Services: append([]application.Service{
			application.NewService(&app.InfoService{}),
			application.NewService(profiles),
			application.NewService(applications.NewService(linux.DesktopCatalog{}, linux.DesktopIcons{OnMainThread: application.InvokeAsync})),
			application.NewService(launcher),
			application.NewService(projectService),
			application.NewService(lifecycle.NewService()),
		}, smokeServices(func() *application.WebviewWindow { return window })...),
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
