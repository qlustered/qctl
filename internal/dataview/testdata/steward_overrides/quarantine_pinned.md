# DATASET SUMMARY
Dataset: work_orders
Clean Rows: 3 | Bad Rows: 3
Page: 1 of 1 | Matching Rows: 3 | Showing: 3 | Steward-Pinned Cells: 3
Legend: [A<id>] = alert <id> flagged that cell, or the whole row in the Issues column | [S] = value pinned by a steward overwrite; what it displaced is under STEWARD OVERRIDES | Row State = valid|invalid|unvalidated (passes_validation_) + quarantine-hold=hold|release (blank = undecided) / waiting-for-approval / held-for-approval=<reason> / suspected-duplicate / awaiting-processing / processing-alert=<id>
Add --include-issues for the message behind each [A<id>]/[W<id>].
Reserved columns — alert_item_ids_, warning_item_ids_ -> the [A<id>]/[W<id>] markers and the Issues column; passes_validation_ -> the Row State column; initial_signature_ -> the ROW SIGNATURES section; hidden: row_number_. Show one with --fields <name>; -o json has them all.

| id | District | Discount | Issues | Row State |
|---|---|---|---|---|
| 1 | East [A8817] [S] | 50.00% | [A8817] district | invalid |
| 2 | West [S] | 25.00% | - | valid |
| 3 | South | 15.00% [S] | - | valid |

## STEWARD OVERRIDES
- 1 district: North → East
- 2 district: (null: was blank, or not recorded) → West
- 3 discount: 1500 → 15.00%

## ROW SIGNATURES
- 1: 9f2ac41b
- 2: 0b77e3d9
- 3: 5c1d0e2a
