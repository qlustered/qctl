package main

import (
	"context"
	"os"

	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/launcher"
)

func main() {
	if err := launcher.New().Run(context.Background()); err != nil {
		errors.Exit(err)
	}
	if err := root.Execute(); err != nil {
		errors.Exit(err)
	}
	os.Exit(0)
}
