package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/instagram/auth"
	"github.com/baptistax/idl/internal/utils"
)

func status(ctx context.Context, cfg config.Config, ui terminal) error {
	start := time.Now()
	ui.banner("System Status")
	var failures []error
	check := func(label string, work func() error) {
		err := ui.wait(ctx, label, work)
		if err != nil {
			failures = append(failures, err)
			if label == "Session file" {
				label += ": " + userMessage(err)
			}
		}
		ui.check(label, err)
	}
	if cfg.Provider == config.GraphProvider {
		check("Graph configuration", func() error { _, err := newProvider(cfg); return err })
	} else {
		check("Session file", func() error {
			_, err := auth.LoadSessionJar(config.ResolveCookiesPath(cfg.CookiesPath))
			return err
		})
	}
	check("Local storage", func() error { return checkStorage(cfg.OutputRoot) })
	ui.warning("Authentication: not verified")
	ui.warning("Instagram connectivity: not checked")
	check("Download engine", func() error { return downloader.CheckEngine(ctx) })
	state := "Limited"
	if len(failures) > 0 {
		state = "Unavailable"
	}
	fmt.Fprintln(ui.out, "\nStatus:", state)
	ui.finished(start)
	return errors.Join(failures...)
}

func checkStorage(root string) error {
	if err := utils.EnsureDir(root); err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".idl-status-*")
	if err != nil {
		return err
	}
	_, err = f.Write([]byte("IDL"))
	return errors.Join(err, f.Close(), os.Remove(f.Name()))
}
