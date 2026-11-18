//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

// UpgradeRow is the table-display projection of one outdated rule instance.
type UpgradeRow struct {
	Table     string `json:"table"`
	TableID   int    `json:"table_id"`
	Rule      string `json:"rule"`
	ID        string `json:"id"`
	On        string `json:"on"`
	UpgradeTo string `json:"upgrade_to"`
}

// ToDisplayList projects the upgrade details onto table rows.
//
// upgrade_to repeats the family's new default release on every row. That is
// deliberate: it keeps a single row self-describing and survives a --columns
// slice, the way `kubectl get pods` repeats NODE.
func ToDisplayList(d *UpgradeDetails) []UpgradeRow {
	newRelease := ""
	if d.NewDefaultRelease != nil {
		newRelease = *d.NewDefaultRelease
	}
	rows := make([]UpgradeRow, 0, len(d.OutdatedItems))
	for _, item := range d.OutdatedItems {
		rows = append(rows, UpgradeRow{
			Table:     item.DatasetName,
			TableID:   item.DatasetID,
			Rule:      item.InstanceName,
			ID:        item.DatasetRuleID.String(),
			On:        item.CurrentRelease,
			UpgradeTo: newRelease,
		})
	}
	return rows
}
