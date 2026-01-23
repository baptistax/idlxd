package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/baptistax/idl/internal/app"
    "github.com/baptistax/idl/internal/config"
)

func main() {
    cfg, err := config.ParseArgs(os.Args[1:])
    if err != nil {
        fmt.Fprintln(os.Stderr, err.Error())
        os.Exit(2)
    }

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    if err := app.Run(ctx, cfg); err != nil {
        fmt.Fprintln(os.Stderr, err.Error())
        os.Exit(1)
    }
}
