package deliverables

import (
	"slices"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/table_series"
)

// ExportManifest exports the deliverable and rev's sheet and cell bindings.
func (c *Client) ExportManifest(token string, item *Deliverable, rev *Revision) (*Manifest, error) {
	if rev == nil {
		return FromAPI(item, rev), nil
	}
	tree, err := c.GetRevision(token, rev.ID)
	if err != nil {
		return nil, err
	}
	return c.exportManifest(token, item, rev, tree.DeliverableRevisionResponse)
}

// exportManifest exports the deliverable and the bindings in tree.
func (c *Client) exportManifest(token string, item *Deliverable, rev *Revision, tree *api.DeliverableRevisionResponse) (*Manifest, error) {
	m := FromAPI(item, rev)
	names, err := c.seriesNames(token, tree)
	if err != nil {
		return nil, err
	}
	m.SetSheets(SheetsFromTree(tree, names))
	cells, err := CellsFromTree(tree, names)
	if err != nil {
		return nil, err
	}
	m.SetCells(cells)
	return m, nil
}

// seriesNames returns the name of each table series, or nil without a request
// when no sheet or cell binding of tree has a series.
func (c *Client) seriesNames(token string, tree *api.DeliverableRevisionResponse) (map[uuid.UUID]string, error) {
	if !slices.ContainsFunc(copied(tree.SheetBindings), func(s api.SheetBindingResponse) bool { return s.MemberSeriesID != nil }) &&
		!slices.ContainsFunc(copied(tree.CellBindings), func(c api.CellBindingResponse) bool { return c.AggregateMemberSeriesID != nil }) {
		return nil, nil
	}
	series, err := table_series.NewClient(c.baseURL, c.orgID.String(), c.verbosity).List(token)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(series))
	for _, item := range series {
		names[item.ID] = item.Name
	}
	return names, nil
}
