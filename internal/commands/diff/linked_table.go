package diff

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/pkg/slug"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// diffLinkedTable previews a LinkedTable apply after server validation.
func diffLinkedTable(cmd *cobra.Command, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	manifest, err := linked_tables.LoadManifest(data)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	ltClient, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	result, _, err := ltClient.Validate(token, manifest)
	if err != nil {
		return fmt.Errorf("failed to validate linked table: %w", err)
	}
	if !result.Valid {
		linked_tables.WriteWarnings(cmd.ErrOrStderr(), result.Warnings)
		return result.Err(manifest.Metadata.Name)
	}

	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	pruned := linked_tables.PrunedOutputColumns(result.Warnings)
	header, changes, err := linkedTableChanges(dsClient, ltClient, token, manifest, pruned)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}

	request, err := manifest.ToAPIRequest()
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to encode request body: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprint(out, header)
	fmt.Fprint(out, datasets.FormatTableFieldChanges(tableFieldChanges(changes), ""))
	// Warnings go to stdout with the rest of the diff: stderr stays empty on
	// exit 1.
	if len(result.Warnings) > 0 {
		fmt.Fprintln(out)
		linked_tables.WriteWarnings(out, result.Warnings)
	}
	fmt.Fprintf(out, "\nrequest body:\n%s\n", terminal.Sanitize(string(body)))
	return qerrors.ErrDiffDifferences
}

// linkedTableChanges returns the changes applying m would make and, when the
// table exists, a header line naming the revision they compare with. Output
// columns in pruned are left out of m.
func linkedTableChanges(dsClient *datasets.Client, ltClient *linked_tables.Client, token string, m *linked_tables.LinkedTableManifest, pruned map[string]bool) (string, []linked_tables.Change, error) {
	name := m.Metadata.Name
	detail, err := linkedTableBySlug(dsClient, ltClient, token, name)
	if err != nil {
		return "", nil, fmt.Errorf("failed to look up linked table %q: %w", name, err)
	}
	if detail == nil {
		changes, err := linked_tables.CreateChanges(m, pruned)
		return "", changes, err
	}
	live := detail.Revision
	ids, names, err := sourceDatasets(dsClient, token, m, live)
	if err != nil {
		return "", nil, err
	}
	changes, err := linked_tables.UpdateChanges(m, live, detail.State, ids, names, pruned)
	if err != nil {
		return "", nil, err
	}
	release := detail.ID.String()
	if live.Release != nil && *live.Release != "" {
		release = *live.Release
	}
	header := terminal.Sanitize(fmt.Sprintf("linked-table/%s: compared with its latest revision, %s (%s)",
		name, release, detail.State))
	return header + "\n", changes, nil
}

// linkedTableBySlug finds the latest revision whose group has name's slug.
func linkedTableBySlug(dsClient *datasets.Client, ltClient *linked_tables.Client, token, name string) (*linked_tables.RevisionDetail, error) {
	wanted := slug.Generate(name, 63)
	seen := map[int]bool{}
	id, found, err := datasetIDByName(dsClient, token, name)
	if err != nil {
		return nil, err
	}
	if found {
		seen[id] = true
		if detail, err := linkedTableRevisionBySlug(ltClient, token, id, wanted); detail != nil || err != nil {
			return detail, err
		}
	}
	return scanLinkedTablesBySlug(dsClient, ltClient, token, wanted, seen)
}

// scanLinkedTablesBySlug searches visible tables for a matching revision group.
func scanLinkedTablesBySlug(dsClient *datasets.Client, ltClient *linked_tables.Client, token, wanted string, seen map[int]bool) (*linked_tables.RevisionDetail, error) {
	count := 0
	for page := 1; ; page++ {
		tables, err := dsClient.GetDatasets(token, datasets.GetDatasetsParams{Page: page, Limit: 100, OrderBy: "id"})
		if err != nil {
			return nil, err
		}
		for _, table := range tables.Results {
			if seen[table.ID] {
				continue
			}
			seen[table.ID] = true
			if detail, err := linkedTableRevisionBySlug(ltClient, token, table.ID, wanted); detail != nil || err != nil {
				return detail, err
			}
		}
		count += len(tables.Results)
		if len(tables.Results) == 0 || tables.TotalRows != nil && count >= *tables.TotalRows || tables.TotalRows == nil && tables.Next == nil {
			return nil, nil
		}
	}
}

// linkedTableRevisionBySlug reads tableID's latest revision when its group matches wanted.
func linkedTableRevisionBySlug(client *linked_tables.Client, token string, tableID int, wanted string) (*linked_tables.RevisionDetail, error) {
	rev, err := client.LatestRevision(token, tableID)
	if qerrors.GetExitCode(err) == qerrors.ExitNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	detail, err := client.GetRevisionDetail(token, rev.ID)
	if err != nil {
		return nil, err
	}
	if detail.Group.Slug == wanted {
		return detail, nil
	}
	return nil, nil
}

// sourceDatasets maps each source dataset name m names to its id, and names
// those ids and every other dataset id live reads from. A live id whose table
// is not found stays unnamed.
func sourceDatasets(client *datasets.Client, token string, m *linked_tables.LinkedTableManifest, live *linked_tables.RevisionSpec) (map[string]int, map[int]string, error) {
	wanted := map[string]bool{}
	for _, src := range m.Spec.Sources {
		wanted[src.Dataset] = true
	}
	ids := make(map[string]int, len(wanted))
	names := map[int]string{}
	for _, name := range slices.Sorted(maps.Keys(wanted)) {
		id, found, err := datasetIDByName(client, token, name)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to look up source table %q: %w", name, err)
		}
		if !found {
			return nil, nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("source table %q not found", name)))
		}
		ids[name] = id
		names[id] = name
	}

	unnamed := map[int]bool{}
	for _, src := range live.Sources {
		if _, ok := names[src.DatasetID]; !ok {
			unnamed[src.DatasetID] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(unnamed)) {
		dataset, err := client.GetDataset(token, id)
		switch {
		case err == nil:
			names[id] = dataset.Name
		case qerrors.GetExitCode(err) != qerrors.ExitNotFound:
			return nil, nil, fmt.Errorf("failed to read source table %d: %w", id, err)
		}
	}
	return ids, names, nil
}

// datasetIDByName returns the id of the table named exactly name, and false
// when none is listed.
func datasetIDByName(client *datasets.Client, token, name string) (int, bool, error) {
	page, err := client.GetDatasets(token, datasets.GetDatasetsParams{Name: &name})
	if err != nil {
		return 0, false, err
	}
	for _, dataset := range page.Results {
		if dataset.Name == name {
			return dataset.ID, true, nil
		}
	}
	return 0, false, nil
}

func tableFieldChanges(changes []linked_tables.Change) []datasets.TableFieldChange {
	converted := make([]datasets.TableFieldChange, len(changes))
	for i, change := range changes {
		converted[i] = datasets.TableFieldChange{
			Path:      change.Path,
			OldValue:  change.Old,
			NewValue:  change.New,
			OldExists: change.Old != nil,
			NewExists: change.New != nil,
		}
	}
	return converted
}
