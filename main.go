package main

import (
	"context"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func main() {
	logger := newLogger()

	sup := NewSupervisor(logger)
	logger.Printf("deepseek-harness-desktop starting")

	app := application.New(application.Options{
		Name:        "DeepSeek Harness",
		Description: "DeepSeek Harness desktop application",
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.easycraft.deepseek-harness-desktop",
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(loadingAssets),
		},
		OnShutdown: sup.Stop,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "DeepSeek Harness",
		Width:     1200,
		Height:    800,
		MinWidth:  720,
		MinHeight: 480,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(e *application.ApplicationEvent) {
		go startup(app, win, sup, logger)
	})

	if err := app.Run(); err != nil {
		logger.Fatal(err)
	}
}

// startup resolves the harness, launches dsh, and points the window at the URL.
// Every failure path shows a dialog (no silent exit from Finder) and quits.
func startup(app *application.App, win *application.WebviewWindow, sup *Supervisor, logger *log.Logger) {
	harnessRoot, err := FindHarness()
	if err != nil {
		logger.Printf("FindHarness: %v", err)
		showError(app, "Could not find the deepseek-harness checkout.\n\n"+err.Error())
		app.Quit()
		return
	}
	logger.Printf("harness root at %s", harnessRoot)

	ctx := context.Background()

	webURL, err := sup.Start(ctx, harnessRoot)
	if err != nil {
		logger.Printf("startup failed: %v", err)
		showError(app, "Failed to start DeepSeek Harness.\n\n"+err.Error())
		app.Quit()
		return
	}

	logger.Printf("dsh web ready at %s", redactToken(webURL))
	win.SetURL(webURL)

	// Register crash watcher: if the child dies unexpectedly, tell the user
	// and quit rather than showing a dead WebView.
	sup.OnUnexpectedExit(func(waitErr error, stderr string) {
		logger.Printf("dsh exited unexpectedly: %v\nstderr:\n%s", waitErr, stderr)
		msg := "DeepSeek Harness server exited unexpectedly."
		if waitErr != nil {
			msg += "\n\n" + waitErr.Error()
		}
		if stderr != "" {
			msg += "\n\nServer output:\n" + stderr
		}
		showError(app, msg)
		app.Quit()
	})
}

// showError shows a modal error dialog; blocks until dismissed.
func showError(app *application.App, message string) {
	dlg := app.Dialog.Error().
		SetTitle("DeepSeek Harness").
		SetMessage(message)
	btn := dlg.AddButton("Quit")
	btn.SetAsDefault()
	dlg.Show()
}
