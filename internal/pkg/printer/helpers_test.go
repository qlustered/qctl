package printer

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestNewPrinterFromCmd tests creating a printer from command flags
func TestNewPrinterFromCmd(t *testing.T) {
	t.Run("default format is table", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if printer.opts.Format != FormatTable {
			t.Errorf("Expected format to be table, got %v", printer.opts.Format)
		}
	})

	t.Run("json format", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "json", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if printer.opts.Format != FormatJSON {
			t.Errorf("Expected format to be json, got %v", printer.opts.Format)
		}
	})

	t.Run("yaml format", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "yaml", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if printer.opts.Format != FormatYAML {
			t.Errorf("Expected format to be yaml, got %v", printer.opts.Format)
		}
	})

	t.Run("name format", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "name", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if printer.opts.Format != FormatName {
			t.Errorf("Expected format to be name, got %v", printer.opts.Format)
		}
	})

	t.Run("invalid format defaults to table", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "invalid", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if printer.opts.Format != FormatTable {
			t.Errorf("Expected format to default to table, got %v", printer.opts.Format)
		}
	})

	t.Run("no-headers flag", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", true, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")
		cmd.Flags().Set("no-headers", "true")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if !printer.opts.NoHeaders {
			t.Errorf("Expected NoHeaders to be true")
		}
	})

	t.Run("columns parsing", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "id,name,email", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")
		cmd.Flags().Set("columns", "id,name,email")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		expectedCols := []string{"id", "name", "email"}
		if len(printer.opts.Columns) != len(expectedCols) {
			t.Errorf("Expected %d columns, got %d", len(expectedCols), len(printer.opts.Columns))
		}

		for i, col := range expectedCols {
			if printer.opts.Columns[i] != col {
				t.Errorf("Expected column[%d] to be %q, got %q", i, col, printer.opts.Columns[i])
			}
		}
	})

	t.Run("columns with whitespace", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "id , name  , email ", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")
		cmd.Flags().Set("columns", "id , name  , email ")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		expectedCols := []string{"id", "name", "email"}
		if len(printer.opts.Columns) != len(expectedCols) {
			t.Errorf("Expected %d columns, got %d", len(expectedCols), len(printer.opts.Columns))
		}

		for i, col := range expectedCols {
			if printer.opts.Columns[i] != col {
				t.Errorf("Expected column[%d] to be %q, got %q", i, col, printer.opts.Columns[i])
			}
		}
	})

	t.Run("empty columns string", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if len(printer.opts.Columns) != 0 {
			t.Errorf("Expected 0 columns for empty string, got %d", len(printer.opts.Columns))
		}
	})

	t.Run("columns with commas only", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", ",,", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")
		cmd.Flags().Set("columns", ",,")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		// Empty columns should be filtered out
		if len(printer.opts.Columns) != 0 {
			t.Errorf("Expected 0 columns, got %d", len(printer.opts.Columns))
		}
	})

	t.Run("allow-plaintext-secrets flag", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "json", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", true, "allow plaintext secrets")
		cmd.Flags().Set("allow-plaintext-secrets", "true")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if !printer.opts.AllowPlaintextSecrets {
			t.Errorf("Expected AllowPlaintextSecrets to be true")
		}
	})

	t.Run("max-column-width flag", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")
		cmd.Flags().Int("max-column-width", 120, "max column width")
		cmd.Flags().Set("max-column-width", "120")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		if printer.opts.MaxColumnWidth != 120 {
			t.Errorf("Expected MaxColumnWidth to be 120, got %d", printer.opts.MaxColumnWidth)
		}
	})

	t.Run("default max-column-width when flag not present", func(t *testing.T) {
		cmd := &cobra.Command{}
		cmd.Flags().String("output", "table", "output format")
		cmd.Flags().Bool("no-headers", false, "disable headers")
		cmd.Flags().String("columns", "", "columns to display")
		cmd.Flags().Bool("allow-plaintext-secrets", false, "allow plaintext secrets")

		printer, err := NewPrinterFromCmd(cmd)
		if err != nil {
			t.Errorf("NewPrinterFromCmd() error = %v", err)
		}

		// Default should be 80
		if printer.opts.MaxColumnWidth != 80 {
			t.Errorf("Expected default MaxColumnWidth to be 80, got %d", printer.opts.MaxColumnWidth)
		}
	})
}
