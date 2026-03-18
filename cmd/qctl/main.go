package main

import (
	"os"

	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/qlustered/qctl/internal/errors"
)

func main() {
	if err := root.Execute(); err != nil {
		errors.Exit(err)
	}
	os.Exit(0)
}
